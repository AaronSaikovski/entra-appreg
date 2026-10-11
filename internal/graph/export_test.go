package graph

// Test-only exports for the external graph_test package.

// ValidateNextLink exposes validateNextLink to graph_test, returning the
// normalized link as a string.
func (c *Client) ValidateNextLink(raw string) (string, error) {
	u, err := c.validateNextLink(raw)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// IsNotFound exposes isNotFound to graph_test.
var IsNotFound = isNotFound
