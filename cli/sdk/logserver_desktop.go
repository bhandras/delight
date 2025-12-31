//go:build !gomobile

package sdk

// StartLogServer starts the HTTP log server and returns its base URL.
func (c *Client) StartLogServer() (string, error) {
	return c.startLogServer()
}
