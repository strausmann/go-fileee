package fileee

func newTagService(c *Client) ReadService[Tag] {
	return &restService[Tag]{client: c, resourcePath: "tags"}
}
