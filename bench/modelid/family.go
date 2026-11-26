package modelid

import "regexp"

// regionPrefix is a Bedrock cross-region inference prefix: us.anthropic...
var regionPrefix = regexp.MustCompile(`^(us|eu|apac|global)\.`)

// Family is the model an id names with every provider wrapping removed:
// lower case, no "provider/" path, no Bedrock region prefix, vendor prefix
// or revision suffix, and no dated snapshot suffix. The Anthropic, Bedrock
// and Vertex ids of one model give the same family, so a test can check that
// its setups compare one model across providers.
func Family(id string) string {
	m := lastSegment(id)
	m = regionPrefix.ReplaceAllString(m, "")
	m = bedrockVendor.ReplaceAllString(m, "")
	m = bedrockRevision.ReplaceAllString(m, "")
	return ownSuffix.ReplaceAllString(m, "")
}
