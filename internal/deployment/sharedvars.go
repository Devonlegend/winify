package deployment

import (
	"fmt"
	"regexp"
	"strings"
)

// sharedVarRef matches a {{project.KEY}} / {{environment.KEY}} reference.
var sharedVarRef = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}`)

// hasSharedRefs reports whether any value contains a shared-variable reference.
func hasSharedRefs(m map[string]string) bool {
	for _, v := range m {
		if sharedVarRef.MatchString(v) {
			return true
		}
	}
	return false
}

// expandSharedVars replaces {{...}} references in every value. An unknown
// reference is an error, so a typo fails the deploy instead of shipping a
// literal placeholder into the container.
func expandSharedVars(m, vars map[string]string) (map[string]string, error) {
	if len(m) == 0 {
		return m, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		expanded, err := expandValue(v, vars)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		out[k] = expanded
	}
	return out, nil
}

func expandValue(v string, vars map[string]string) (string, error) {
	var refErr error
	out := sharedVarRef.ReplaceAllStringFunc(v, func(match string) string {
		name := strings.TrimSpace(sharedVarRef.FindStringSubmatch(match)[1])
		value, ok := vars[name]
		if !ok {
			refErr = fmt.Errorf("unknown shared variable %q", name)
			return match
		}
		return value
	})
	if refErr != nil {
		return "", refErr
	}
	return out, nil
}
