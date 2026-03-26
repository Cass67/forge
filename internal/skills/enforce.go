package skills

// RequiredForInput returns the name of a skill that should be activated before
// handling the given user input, if any.
func RequiredForInput(input string) string {
	return resolveRequiredSkillName(input)
}
