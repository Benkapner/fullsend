//go:build !github

package mintcore

// statusValidators returns the optional status auth validators.
// Without the github build tag, no optional validators are active.
// The slice is empty; OIDC is the only auth path.
func statusValidators() []StatusValidator {
	return nil
}
