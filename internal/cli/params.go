package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
)

// maxParamsFile bounds how much of --params-file is read.
const maxParamsFile = 1 << 20

// textOption binds a long option name (without "--") to the Options field
// that holds its text value.
type textOption struct {
	name   string
	target **string
}

// textOptions lists every text option except --params-file. It is the single
// source for both the command-line flags and the --params-file keys.
func textOptions(o *Options) []textOption {
	return []textOption{
		{"appid", &o.AppID},
		{"name", &o.Name},
		{"audience", &o.Audience},
		{"scope-name", &o.ScopeName},
		{"scope-display-name", &o.ScopeDisplayName},
		{"scope-description", &o.ScopeDescription},
		{"scope-consent", &o.ScopeConsent},
		{"scope-user-display-name", &o.ScopeUserDisplayName},
		{"scope-user-description", &o.ScopeUserDescription},
		{"scope-state", &o.ScopeState},
		{"top", &o.Top},
		{"secret-expiry", &o.SecretExpiry},
		{"secret-start", &o.SecretStart},
		{"secret-end", &o.SecretEnd},
		{"keyvault-uri", &o.KeyVaultURI},
		{"keyvault-secret-name", &o.KeyVaultSecretName},
	}
}

// applyParamsFile fills every option not given on the command line from the
// JSON object in path. Command-line values win, including redirect lists.
// JSON null counts as absent. Keys are checked in sorted order, so the
// reported error is stable when several keys are wrong.
func applyParamsFile(o *Options, path string) error {
	params, err := readParamsFile(path)
	if err != nil {
		return err
	}
	targets := make(map[string]**string)
	for _, t := range textOptions(o) {
		targets[t.name] = t.target
	}
	for _, key := range slices.Sorted(maps.Keys(params)) {
		value := params[key]
		if string(value) == "null" {
			continue
		}
		var bad string // expected type when value does not decode
		switch key {
		case "redirect-urls":
			list, ok := decodeList(value)
			if !ok {
				bad = "a string or an array of strings"
			} else if len(o.RedirectURIs) == 0 {
				o.RedirectURIs = list
			}
		case "create-secret", "json":
			var b bool
			if json.Unmarshal(value, &b) != nil {
				bad = "true or false"
			} else if key == "json" {
				o.JSON = o.JSON || b
			} else {
				o.CreateSecret = o.CreateSecret || b
			}
		default:
			target, known := targets[key]
			if !known {
				return Usagef("--params-file %s: unknown key %q. Keys are option names without the leading '--'.", path, key)
			}
			numeric := key == "top" || key == "secret-expiry"
			text, ok := decodeText(value, numeric)
			if !ok {
				bad = "a string"
				if numeric {
					bad = "a string or a whole number"
				}
			} else if *target == nil {
				*target = &text
			}
		}
		if bad != "" {
			return Usagef("--params-file %s: %q must be %s.", path, key, bad)
		}
	}
	return nil
}

// readParamsFile reads at most maxParamsFile bytes from path and decodes
// exactly one JSON object, keeping each value raw for per-key decoding.
func readParamsFile(path string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(path) == "" {
		return nil, Usagef("--params-file cannot be blank.")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, Usagef("Could not read --params-file: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxParamsFile+1))
	if err != nil {
		return nil, Usagef("Could not read --params-file: %v", err)
	}
	if len(data) > maxParamsFile {
		return nil, Usagef("--params-file %s is larger than %d bytes.", path, maxParamsFile)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var params map[string]json.RawMessage
	if err := dec.Decode(&params); err != nil {
		if _, isType := errors.AsType[*json.UnmarshalTypeError](err); isType {
			return nil, Usagef("--params-file %s must contain a JSON object.", path)
		}
		return nil, Usagef("--params-file %s is not valid JSON: %v", path, err)
	}
	if params == nil {
		return nil, Usagef("--params-file %s must contain a JSON object.", path)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, Usagef("--params-file %s must contain exactly one JSON object.", path)
	}
	return params, nil
}

// decodeText accepts a JSON string, or a whole JSON number when numeric is
// set, so "top": 10 and "secret-expiry": 90 read naturally.
func decodeText(value json.RawMessage, numeric bool) (string, bool) {
	var s string
	if json.Unmarshal(value, &s) == nil {
		return s, true
	}
	if !numeric {
		return "", false
	}
	var n json.Number
	if json.Unmarshal(value, &n) != nil {
		return "", false
	}
	if _, err := strconv.ParseInt(n.String(), 10, 64); err != nil {
		return "", false
	}
	return n.String(), true
}

// decodeList accepts one string or an array of strings. Either form is then
// handled like raw --redirect-urls values, so a string may hold a comma list.
func decodeList(value json.RawMessage) ([]string, bool) {
	var s string
	if json.Unmarshal(value, &s) == nil {
		return []string{s}, true
	}
	var list []string
	if json.Unmarshal(value, &list) != nil {
		return nil, false
	}
	return list, true
}
