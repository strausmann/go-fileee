package fileee

func newCompanyService(c *Client) ReadService[Company] {
	return &restService[Company]{client: c, resourcePath: "companies"}
}
