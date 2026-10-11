package graph

// Test-only exports for the external graph_test package.

// ValidateNextLink exposes validateNextLink to graph_test.
func (c *Client) ValidateNextLink(raw string) (string, error) { return c.validateNextLink(raw) }

// IsNotFound exposes isNotFound to graph_test.
var IsNotFound = isNotFound
