package cpyaml

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// WHAT AN OMITTED FIELD BECOMES. A deploy gives a field the file leaves out its
// default in the control plane's AetherfyConfig, never the agent's previous
// value, so `afy diff` compares an omitted field with that default. The CLI
// holds a copy (yamldiff.Defaults), and this reads the model's, so the two are
// compared rather than trusted (archive's TestKnownFieldsEqualTheControlPlaneModel).

var (
	// A Python string literal, for blanking strings out before counting parens.
	pyString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
	// The default a declaration gives, in the forms config_parser.py uses.
	defaultKw      = regexp.MustCompile(`\bdefault\s*=\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|None|True|False|-?[0-9]+)\s*[,)]`)
	defaultFactory = regexp.MustCompile(`\bdefault_factory\s*=\s*([A-Za-z_]+)\s*[,)]`)
	requiredField  = regexp.MustCompile(`=\s*Field\(\s*\.\.\.\s*[,)]`)
	plainDefault   = regexp.MustCompile(`^[^=]*=\s*("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|None|True|False|-?[0-9]+)\s*(?:#.*)?$`)
)

// Defaults reads RootModel's fields' defaults from SourcePath under cpRoot,
// decoded the way yaml.v3 decodes the same value in a file: None -> nil, a
// bool, an int, a string, list() -> []interface{}{}, dict() -> an empty map.
// A required field (Field(...)) has no entry and is listed in required.
//
// It REFUSES a field whose default it cannot read (a call, a name, an
// expression): leaving it out would compare the CLI's copy against less than
// the model and agree by omission.
func Defaults(cpRoot string) (defaults map[string]interface{}, required []string, err error) {
	raw, err := os.ReadFile(filepath.Join(cpRoot, filepath.FromSlash(SourcePath)))
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	decls := map[string]string{}
	var order []string
	in, inDoc := false, false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if m := classDecl.FindStringSubmatch(l); m != nil {
			in = m[1] == RootModel
			inDoc = false
			continue
		}
		if !in {
			continue
		}
		if l != "" && !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "#") {
			break // column 0: the class body has ended
		}
		if n := strings.Count(l, `"""`); n > 0 {
			if n == 1 {
				inDoc = !inDoc
			}
			continue
		}
		if inDoc {
			continue
		}
		m := fieldDecl.FindStringSubmatch(l)
		if m == nil || m[1] == "model_config" {
			continue
		}
		// The whole declaration: continue while its parentheses are open.
		text := l
		depth := parenDepth(l)
		for depth > 0 && i+1 < len(lines) {
			i++
			text += "\n" + lines[i]
			depth += parenDepth(lines[i])
		}
		decls[m[1]] = text
		order = append(order, m[1])
	}
	if len(order) == 0 {
		return nil, nil, fmt.Errorf("no fields of %s found in %s", RootModel, SourcePath)
	}

	defaults = map[string]interface{}{}
	for _, name := range order {
		text := decls[name]
		switch {
		case requiredField.MatchString(text):
			required = append(required, name)
		case defaultFactory.MatchString(text):
			switch f := defaultFactory.FindStringSubmatch(text)[1]; f {
			case "list":
				defaults[name] = []interface{}{}
			case "dict":
				defaults[name] = map[string]interface{}{}
			default:
				return nil, nil, fmt.Errorf("%s.%s has default_factory=%s, which this reader does not decode", RootModel, name, f)
			}
		case defaultKw.MatchString(text):
			v, err := pyLiteral(defaultKw.FindStringSubmatch(text)[1])
			if err != nil {
				return nil, nil, fmt.Errorf("%s.%s: %v", RootModel, name, err)
			}
			defaults[name] = v
		case !strings.Contains(text, "Field(") && plainDefault.MatchString(text):
			v, err := pyLiteral(plainDefault.FindStringSubmatch(text)[1])
			if err != nil {
				return nil, nil, fmt.Errorf("%s.%s: %v", RootModel, name, err)
			}
			defaults[name] = v
		default:
			return nil, nil, fmt.Errorf("%s.%s: cannot read its default from %q -- teach this reader the shape "+
				"rather than leave the field out", RootModel, name, strings.TrimSpace(text))
		}
	}
	return defaults, required, nil
}

// MissingConfigMessage reads the control plane's MISSING_CONFIG_MESSAGE from
// SourcePath: `NAME = (` then string literals, one per line, concatenated,
// then `)`. Anything else inside is refused, so a sentence assembled some
// other way is not read as a shorter one.
func MissingConfigMessage(cpRoot string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(cpRoot, filepath.FromSlash(SourcePath)))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "MISSING_CONFIG_MESSAGE = (" {
			continue
		}
		var b strings.Builder
		for _, part := range lines[i+1:] {
			part = strings.TrimSpace(part)
			if part == ")" {
				if b.Len() == 0 {
					return "", fmt.Errorf("MISSING_CONFIG_MESSAGE in %s is empty", SourcePath)
				}
				return b.String(), nil
			}
			s, err := strconv.Unquote(part)
			if err != nil {
				return "", fmt.Errorf("MISSING_CONFIG_MESSAGE has a line this reader cannot read: %q", part)
			}
			b.WriteString(s)
		}
		return "", fmt.Errorf("MISSING_CONFIG_MESSAGE in %s never closes", SourcePath)
	}
	return "", fmt.Errorf("no `MISSING_CONFIG_MESSAGE = (` in %s", SourcePath)
}

// parenDepth is the net count of ( over ) on a line, string literals ignored.
func parenDepth(l string) int {
	s := pyString.ReplaceAllString(l, `""`)
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[:i]
	}
	return strings.Count(s, "(") - strings.Count(s, ")")
}

func pyLiteral(lit string) (interface{}, error) {
	switch {
	case lit == "None":
		return nil, nil
	case lit == "True":
		return true, nil
	case lit == "False":
		return false, nil
	case strings.HasPrefix(lit, `"`) || strings.HasPrefix(lit, `'`):
		return lit[1 : len(lit)-1], nil
	}
	n, err := strconv.Atoi(lit)
	if err != nil {
		return nil, fmt.Errorf("default %s is not a literal this reader decodes", lit)
	}
	return n, nil
}
