// Code generation pipeline for the RAIL0 Go SDK.
//
// Run from the repository root with:
//
//	go run gen/generate.go
//
// Schema source (first match wins):
//  1. RAIL0_SCHEMA_PATH env var — absolute path to a local openapi.json
//  2. Default: ../rail0-gateway/docs/openapi.json  (sibling gateway repo)
//
// What it emits:
//   - types_gen.go ONLY — the request/response data types derived from the
//     gateway's components.schemas. This is the single source of truth for the
//     SDK's domain types. Do NOT hand-edit types_gen.go.
//   - In the same file, the dispute-reason dictionaries (see reasonDictionaries):
//     one DisputeReason constant per code plus a []DisputeReasonInfo table, read
//     from the enum schemas' x-enum-descriptions / x-enum-bytes32 extensions.
//
// What it does NOT emit:
//   - The client method files (accounts.go, payments.go, chains.go, tokens.go,
//     auth.go, webhooks.go, health.go). Those are hand-maintained and aligned to
//     the gateway API surface; the generator must never overwrite them.
//
// OpenAPI 3.1 handling:
//   - `type` may be a JSON string ("string") or an array (["string","null"]).
//     A "null" member marks the property nullable.
//   - `allOf` is treated as inheritance: a schema that is allOf[$ref(Base), {…}]
//     is flattened into a single struct that inlines the base's properties first,
//     then its own. This matches the SDK convention of exposing one struct per
//     resource (e.g. Payment carries the PaymentDetail extras; Webhook carries the
//     WebhookWithSecret shared_secret).
//
// Add future generation steps here.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/template"
	"time"
	"unicode"
)

// ================================================================
//  Minimal OpenAPI 3.1 types (only the subset we need)
// ================================================================

// Schema models the subset of an OpenAPI 3.1 schema object we consume.
//
// Type is json.RawMessage because OpenAPI 3.1 allows `type` to be either a
// single string ("string") or an array of strings (["string","null"]); we
// decode it lazily in typeInfo.
type Schema struct {
	Type        json.RawMessage    `json:"type"`
	Format      string             `json:"format"`
	Description string             `json:"description"`
	Properties  map[string]*Schema `json:"properties"`
	Required    []string           `json:"required"`
	Ref         string             `json:"$ref"`
	AllOf       []*Schema          `json:"allOf"`
	Enum        enumValues         `json:"enum"`
	Items       *Schema            `json:"items"`
	// Index-aligned with Enum on the gateway's reason-dictionary schemas.
	EnumDescriptions []string `json:"x-enum-descriptions"`
	EnumBytes32      []string `json:"x-enum-bytes32"`
}

// enumValues decodes an `enum` whose members are not all strings. The generator
// reads enum members only as the codes of the reason dictionaries (strings), but
// the spec also carries constant-valued fields — QueuedEmail.queued is
// `{"type": "boolean", "enum": [true]}` — and a plain []string made the whole
// schema fail to parse. A null member decodes to "" (as it did before); any other
// non-string is kept in its JSON spelling, which is never used as a code.
type enumValues []string

func (e *enumValues) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(enumValues, 0, len(raw))
	for _, r := range raw {
		var s string
		switch {
		case string(r) == "null":
			s = ""
		case json.Unmarshal(r, &s) == nil:
		default:
			s = string(r)
		}
		out = append(out, s)
	}
	*e = out
	return nil
}

type OpenAPI struct {
	Components struct {
		Schemas map[string]*Schema `json:"schemas"`
	} `json:"components"`
}

// typeInfo decodes the (possibly array) `type` keyword into a primary type plus
// a nullable flag. OpenAPI 3.1 expresses "string or null" as ["string","null"].
func (s *Schema) typeInfo() (primary string, nullable bool) {
	if len(s.Type) == 0 {
		return "", false
	}
	// Try a bare string first.
	var single string
	if err := json.Unmarshal(s.Type, &single); err == nil {
		return single, false
	}
	// Otherwise it's an array of types.
	var arr []string
	if err := json.Unmarshal(s.Type, &arr); err == nil {
		for _, t := range arr {
			if t == "null" {
				nullable = true
				continue
			}
			if primary == "" {
				primary = t
			}
		}
	}
	return primary, nullable
}

// ================================================================
//  SDK-specific generation config
// ================================================================

// primitiveAliases maps a component-schema name to a Go type alias. These are
// emitted as `type X = base` aliases rather than structs. (Currently the
// gateway schema carries no primitive component schemas; the aliases the SDK
// relies on — Address, Bytes32, Uint256String — are hand-kept in types.go
// alongside the EIP-712 signing types. Kept here for forward compatibility.)
var primitiveAliases = map[string]string{
	"Address":       "string",
	"Bytes32":       "string",
	"Uint256String": "string",
}

// schemaTypeOverride maps an exact (SchemaName, jsonProperty) to a Go type that
// overrides the default mapping, so generated types match the aligned client's
// chosen aliases and ergonomics. Empty value means "use default".
var schemaTypeOverride = map[string]map[string]string{
	// Payment carries both the base Payment fields and the merged PaymentDetail
	// extras, so its overrides cover both (merge uses the canonical name).
	"Payment": {
		"rail0_id":             "Bytes32",
		"config_hash":          "Bytes32",
		"amount":               "Uint256String",
		"payer":                "Address",
		"payee":                "Address",
		"token":                "Address",
		"rail0_contract":       "Address",
		"signing_payload":      "*SigningPayload",
		"authorization_expiry": "int64",
		"refund_expiry":        "int64",
		// Nullable: null when the gateway cannot resolve the token (gateway #367), so
		// a pointer keeps "unknown" apart from a real 0-decimals token.
		"decimals": "*int",
	},
	"Transaction": {
		"amount":           "Uint256String",
		"transaction_hash": "*Bytes32",
		"block_number":     "*int",
	},
	"Webhook": {
		// A SET of topics since gateway #231, and still []string rather than
		// []WebhookTopic for the same reason the singular was string: standalone enum
		// schemas are skipped below and surface as the Topic* string constants, so a
		// named element type would be undefined.
		"topics": "[]string",
	},
	"BlockchainSettlement": {
		// Null below the gateway's minimum sample (gateway #365); a pointer keeps
		// "not measured yet" apart from a real 0.
		"p50_seconds": "*int",
		"p90_seconds": "*int",
	},
	"EventCallback": {
		"response_code": "*int",
	},
	"Token": {
		"address": "Address",
	},
	"Dispute": {
		// anyOf[$ref enum, null] in the spec, which goType would read as `any`. The
		// codes are the reason dictionaries' (see reasonDictionaries); a null code
		// (a bytes32 outside the dictionary) decodes to "".
		"reason_code":       "DisputeReason",
		"close_reason_code": "DisputeReason",
	},
}

// reasonDictionaries lists the enum schemas emitted as dispute-reason dictionaries,
// in output order: Schema names the component schema, Table the exported
// []DisputeReasonInfo variable. Each code becomes a constant named Schema+Code
// (DisputeOpenReasonNotReceived), so a code present in two lists (`other`) still
// gets two distinct names. The DisputeReason/DisputeReasonInfo types and the lookup
// helpers are hand-kept in disputes.go.
var reasonDictionaries = []struct{ Schema, Table string }{
	{"DisputeOpenReason", "DisputeOpenReasons"},
	{"DisputeCloseReason", "DisputeCloseReasons"},
	{"DisputeSystemCloseReason", "DisputeSystemCloseReasons"},
}

// fieldNameOverride pins the exported Go name for specific (Schema, jsonProperty)
// pairs where the default initialism rules would diverge from the aligned
// client's public field name. Keeps the SDK API stable across regeneration.
var fieldNameOverride = map[string]map[string]string{
	"Payment": {"rail0_id": "Rail0Id"},
}

// extraFields injects SDK-only fields onto a generated struct that have no
// counterpart in the OpenAPI schema (e.g. payloads the SDK threads through but
// the gateway does not document as a discrete property).
var extraFields = map[string][]Field{
	"Transaction": {
		{
			Name:    "SigningPayload",
			Type:    "*SigningPayload",
			JSON:    "signing_payload",
			Pointer: true,
			Doc:     "SigningPayload is returned by the refund prepare phase-1 step (no signature supplied yet) so the payee can sign the EIP-3009 authorization. SDK-only; not part of the OpenAPI Transaction schema.",
		},
	},
}

// allOfMergeParent lists schemas whose allOf base should be flattened INTO the
// derived schema, and whether the derived schema should keep the parent's name.
// We merge PaymentDetail's extras into Payment and WebhookWithSecret's extras
// into Webhook so the SDK exposes one struct per resource (matching the aligned
// client). The derived-only schema name is then skipped from output.
//
// key = derived schema name, value = the canonical struct name to merge into.
var mergeInto = map[string]string{
	"PaymentDetail":     "Payment",
	"WebhookWithSecret": "Webhook",
}

// skipSchemas are component schemas the generator must NOT emit as Go types,
// because the aligned client hand-maintains an equivalent (often renamed or
// reshaped) type. Health/Session/Nonce are exposed by the client as
// HealthResponse/AuthResponse/NonceResponse with hand-tuned fields.
//
// Account is hand-maintained in accounts.go for the same reason, and so is
// PaymentWindows, whose per-window objects would otherwise generate as `any`. AdminContract is
// skipped because it is admin-only: the SDK tracks the public surface, and the
// operator's deployment bookkeeping is rail0-admin's concern, not a client's.
var skipSchemas = map[string]bool{
	"Health":         true,
	"Session":        true,
	"Nonce":          true,
	"Account":        true,
	"PaymentWindows": true,
	"AdminContract":  true,
}

// ================================================================
//  Type mapping
// ================================================================

// goType resolves an OpenAPI schema to a Go type expression.
func goType(s *Schema) string {
	// $ref → use the referenced schema name directly (resolving merge targets).
	if s.Ref != "" {
		name := refName(s.Ref)
		if canon, ok := mergeInto[name]; ok {
			return canon
		}
		return name
	}
	// allOf with a single $ref attaches a description to a ref — unwrap it.
	if len(s.AllOf) == 1 && s.AllOf[0].Ref != "" {
		return refName(s.AllOf[0].Ref)
	}
	primary, _ := s.typeInfo()
	switch primary {
	case "string":
		return "string"
	case "boolean":
		return "bool"
	case "integer":
		if s.Format == "int64" {
			return "int64"
		}
		return "int"
	case "number":
		return "float64"
	case "array":
		if s.Items != nil {
			return "[]" + goType(s.Items)
		}
		return "[]any"
	case "object", "":
		return "any"
	default:
		return "any"
	}
}

func refName(ref string) string {
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

// exported converts a JSON property name (snake_case) to an exported Go name
// (PascalCase), upper-casing common initialisms for idiomatic Go.
func exported(name string) string {
	if name == "" {
		return ""
	}
	parts := strings.Split(name, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		if up, ok := initialisms[strings.ToLower(p)]; ok {
			b.WriteString(up)
			continue
		}
		r := []rune(p)
		r[0] = unicode.ToUpper(r[0])
		b.WriteString(string(r))
	}
	return b.String()
}

// initialisms is the set of segments that should be fully upper-cased to match
// Go naming conventions and the existing hand-written field names.
var initialisms = map[string]string{
	"id":   "ID",
	"url":  "URL",
	"api":  "API",
	"db":   "DB",
	"uuid": "UUID",
}

// ================================================================
//  Template data structures
// ================================================================

type Field struct {
	Name    string // exported Go name
	Type    string // Go type
	JSON    string // original JSON key
	Doc     string // inline doc comment (may be empty)
	Pointer bool   // true when the field is optional (emits ,omitempty)
}

type Struct struct {
	Name   string
	Doc    string
	Fields []Field
}

type EnumConst struct {
	Doc    string
	Values []EnumValue
}

type EnumValue struct {
	Name  string
	Value string
}

type Alias struct {
	Name string
	Base string
	Doc  string
}

// ReasonDict is one dispute-reason dictionary ready for the template.
type ReasonDict struct {
	Schema  string
	Table   string
	Doc     string
	Entries []ReasonEntry
}

type ReasonEntry struct {
	Const       string
	Code        string
	Description string
	Bytes32     string
}

type GenData struct {
	Timestamp string
	Aliases   []Alias
	Structs   []Struct
	Reasons   []ReasonDict
}

// ================================================================
//  Code template — types_gen.go
// ================================================================

const goTypesTemplate = `// Code generated by gen/generate.go on {{ .Timestamp }}; DO NOT EDIT.
// Source: rail0-gateway/docs/openapi.json
//
// To regenerate after changing the gateway OpenAPI spec:
//   go run gen/generate.go
//
// This file holds ONLY the domain request/response types derived from the
// gateway's components.schemas, plus the dispute-reason dictionaries (codes,
// descriptions, bytes32). The client method files, the EIP-712 signing helper
// types and the DisputeReason/DisputeReasonInfo types are hand-maintained elsewhere.

package rail0
{{ range .Aliases }}
// {{ .Name }} is a type alias for {{ .Base }}.{{ if .Doc }} {{ .Doc }}{{ end }}
type {{ .Name }} = {{ .Base }}
{{ end }}{{ range .Structs }}
// {{ .Name }}{{ if .Doc }} {{ .Doc }}{{ end }}
type {{ .Name }} struct {
{{ range .Fields }}	{{ if .Doc }}// {{ .Doc }}
	{{ end }}{{ .Name }} {{ .Type }} ` + "`" + `json:"{{ .JSON }}{{ if .Pointer }},omitempty{{ end }}"` + "`" + `
{{ end }}}
{{ end }}{{ range .Reasons }}
// {{ .Schema }} codes. {{ .Doc }}
const (
{{ range .Entries }}	// {{ .Const }}: {{ .Description }}
	{{ .Const }} DisputeReason = {{ printf "%q" .Code }}
{{ end }})

// {{ .Table }} is the {{ .Schema }} dictionary in the gateway's order: each code with
// its description and its on-chain bytes32.
var {{ .Table }} = []DisputeReasonInfo{
{{ range .Entries }}	{Code: {{ .Const }}, Description: {{ printf "%q" .Description }}, Bytes32: {{ printf "%q" .Bytes32 }}},
{{ end }}}
{{ end }}
// PageLinks holds the RFC 8288 Link header's relations. first and last are always
// present, prev and next only where they exist, and all four are empty on an empty
// collection (the gateway omits the header rather than sending one pointing nowhere).
// The URIs are RELATIVE - path and query - and resolve against the URL you requested:
// the gateway emits them that way so they cannot advertise the wrong scheme through a
// TLS-terminating proxy.
type PageLinks struct {
	First string ` + "`" + `json:"first,omitempty"` + "`" + `
	Prev  string ` + "`" + `json:"prev,omitempty"` + "`" + `
	Next  string ` + "`" + `json:"next,omitempty"` + "`" + `
	Last  string ` + "`" + `json:"last,omitempty"` + "`" + `
}

// PageMeta holds pagination metadata returned by list endpoints.
type PageMeta struct {
	Page    int ` + "`" + `json:"page"` + "`" + `
	PerPage int ` + "`" + `json:"per_page"` + "`" + `
	Total   int ` + "`" + `json:"total"` + "`" + `
	// TotalPages is zero for an empty collection: "no pages" is what there are,
	// so a pager rendered off it renders none.
	TotalPages int ` + "`" + `json:"total_pages"` + "`" + `
	Links      PageLinks ` + "`" + `json:"links"` + "`" + `
}

// PaginatedResponse is a generic paginated list response.
type PaginatedResponse[T any] struct {
	Data []T      ` + "`" + `json:"data"` + "`" + `
	Meta PageMeta ` + "`" + `json:"meta"` + "`" + `
}
`

// ================================================================
//  Main
// ================================================================

// resolveSchemaPath returns the path to openapi.json to use.
func resolveSchemaPath(rootDir string) string {
	if p := os.Getenv("RAIL0_SCHEMA_PATH"); p != "" {
		return p
	}
	return filepath.Join(rootDir, "..", "rail0-gateway", "docs", "openapi.json")
}

func main() {
	_, thisFile, _, _ := runtime.Caller(0)
	genDir := filepath.Dir(thisFile)
	rootDir := filepath.Dir(genDir)

	schemaPath := resolveSchemaPath(rootDir)
	fmt.Printf("Reading schema: %s\n", schemaPath)

	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading schema: %v\n", err)
		os.Exit(1)
	}

	var api OpenAPI
	if err := json.Unmarshal(raw, &api); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing schema: %v\n", err)
		os.Exit(1)
	}

	typesPath := filepath.Join(rootDir, "types_gen.go")
	data := buildData(api)
	reasons, err := buildReasons(api)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reason dictionaries: %v\n", err)
		os.Exit(1)
	}
	data.Reasons = reasons
	tmpl, err := template.New("types").Parse(goTypesTemplate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "template parse error: %v\n", err)
		os.Exit(1)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		fmt.Fprintf(os.Stderr, "template execute error: %v\n", err)
		os.Exit(1)
	}
	// gofmt the output so the committed file is canonically formatted.
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		fmt.Fprintf(os.Stderr, "gofmt error: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(typesPath, formatted, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", typesPath, err)
		os.Exit(1)
	}
	fmt.Printf("Generated: %s\n", typesPath)
	fmt.Println("Done.")
}

// fields turns a schema's properties into ordered template Fields. requiredSet
// marks which properties are required (non-pointer, no omitempty). The schema
// name is used to look up per-field type overrides.
func fields(schemaName string, props map[string]*Schema, requiredSet map[string]bool) []Field {
	names := make([]string, 0, len(props))
	for pn := range props {
		names = append(names, pn)
	}
	// Required fields first, then optional; each group alphabetical for stable output.
	slices.SortFunc(names, func(a, b string) int {
		aReq, bReq := requiredSet[a], requiredSet[b]
		if aReq != bReq {
			if aReq {
				return -1
			}
			return 1
		}
		return strings.Compare(a, b)
	})

	overrides := schemaTypeOverride[schemaName]
	nameOverrides := fieldNameOverride[schemaName]
	out := make([]Field, 0, len(names))
	for _, pn := range names {
		prop := props[pn]
		optional := !requiredSet[pn]
		_, nullable := prop.typeInfo()

		ft := goType(prop)
		if ov, ok := overrides[pn]; ok && ov != "" {
			ft = ov
		}

		goName := exported(pn)
		if no, ok := nameOverrides[pn]; ok && no != "" {
			goName = no
		}

		// Convention (matches the hand-aligned client): optional/nullable fields
		// are emitted as VALUE types with `,omitempty`, not pointers. The SDK
		// reserves pointers for the few response fields where distinguishing
		// "absent" from the zero value is semantically required — those are
		// pinned explicitly via schemaTypeOverride (e.g. *Bytes32, *int).
		_ = nullable
		out = append(out, Field{
			Name:    goName,
			Type:    ft,
			JSON:    pn,
			Doc:     prop.Description,
			Pointer: (optional || nullable) && !neverOmitted(ft),
		})
	}
	return out
}

// neverOmitted reports whether a field of Go type ft must be serialised even at its
// zero value. A bool is: every generated type is a gateway RESPONSE, and there
// `false` is an answer, not an absence. With `,omitempty` a `disputed: false` or an
// inactive wallet's `active: false` vanished from any re-encoded value — the CLI's
// JSON output among them — so `jq .disputed` read null and an inactive wallet showed
// no `active` key at all. Request bodies are hand-written (with *bool where "unset"
// matters), so this cannot make a request send a false nobody asked for.
func neverOmitted(ft string) bool { return ft == "bool" }

// buildData converts the OpenAPI schemas into template data, applying the SDK
// generation conventions (aliases, allOf merging, skips, type overrides).
func buildData(api OpenAPI) GenData {
	var aliases []Alias
	structByName := map[string]*Struct{}
	var order []string

	names := make([]string, 0, len(api.Components.Schemas))
	for name := range api.Components.Schemas {
		names = append(names, name)
	}
	slices.Sort(names)

	// First pass: emit base structs and aliases (skip merge-derived and skipped).
	for _, name := range names {
		if skipSchemas[name] || mergeInto[name] != "" {
			continue
		}
		s := api.Components.Schemas[name]

		if alias, ok := primitiveAliases[name]; ok {
			aliases = append(aliases, Alias{Name: name, Base: alias, Doc: s.Description})
			continue
		}

		primary, _ := s.typeInfo()
		// Skip standalone enum/string schemas (e.g. WebhookTopic): the client
		// exposes those as string constants, not Go types.
		if primary != "object" {
			continue
		}

		requiredSet := make(map[string]bool, len(s.Required))
		for _, r := range s.Required {
			requiredSet[r] = true
		}
		st := &Struct{Name: name, Doc: s.Description, Fields: fields(name, s.Properties, requiredSet)}
		structByName[name] = st
		order = append(order, name)
	}

	// Second pass: merge allOf-derived schemas into their canonical base struct.
	for _, name := range names {
		canon, ok := mergeInto[name]
		if !ok {
			continue
		}
		derived := api.Components.Schemas[name]
		target := structByName[canon]
		if target == nil {
			continue
		}
		for _, part := range derived.AllOf {
			if part.Ref != "" {
				continue // the base ref — already represented by the canonical struct
			}
			requiredSet := make(map[string]bool, len(part.Required))
			for _, r := range part.Required {
				requiredSet[r] = true
			}
			target.Fields = append(target.Fields, fields(canon, part.Properties, requiredSet)...)
		}
	}

	// Inject SDK-only extra fields.
	for name, extras := range extraFields {
		if st := structByName[name]; st != nil {
			st.Fields = append(st.Fields, extras...)
		}
	}

	structs := make([]Struct, 0, len(order))
	for _, name := range order {
		structs = append(structs, *structByName[name])
	}

	return GenData{
		Timestamp: time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		Aliases:   aliases,
		Structs:   structs,
	}
}

// buildReasons reads each reasonDictionaries schema into template data. The three
// arrays (enum, x-enum-descriptions, x-enum-bytes32) are aligned by index in the
// spec; a missing schema or a length mismatch fails the run instead of emitting a
// dictionary whose descriptions or hashes sit against the wrong code.
func buildReasons(api OpenAPI) ([]ReasonDict, error) {
	out := make([]ReasonDict, 0, len(reasonDictionaries))
	for _, d := range reasonDictionaries {
		s := api.Components.Schemas[d.Schema]
		if s == nil {
			return nil, fmt.Errorf("schema %s not found", d.Schema)
		}
		if len(s.Enum) == 0 || len(s.EnumDescriptions) != len(s.Enum) || len(s.EnumBytes32) != len(s.Enum) {
			return nil, fmt.Errorf("schema %s: enum (%d), x-enum-descriptions (%d) and x-enum-bytes32 (%d) must be non-empty and the same length",
				d.Schema, len(s.Enum), len(s.EnumDescriptions), len(s.EnumBytes32))
		}
		// The description's first paragraph only: the rest is a bulleted copy of the
		// codes, which the per-constant comments already carry.
		doc, _, _ := strings.Cut(s.Description, "\n\n")
		rd := ReasonDict{Schema: d.Schema, Table: d.Table, Doc: doc}
		for i, code := range s.Enum {
			rd.Entries = append(rd.Entries, ReasonEntry{
				Const:       d.Schema + exported(code),
				Code:        code,
				Description: s.EnumDescriptions[i],
				Bytes32:     s.EnumBytes32[i],
			})
		}
		out = append(out, rd)
	}
	return out, nil
}
