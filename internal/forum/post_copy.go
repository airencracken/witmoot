package forum

// postBody keeps historical storage placeholders readable without changing
// the exact value required by the removed-post database constraint.
func postBody(body string, removed bool, author string) string {
	if removed {
		return "This post was removed by a site owner."
	}
	if author == "Former member" && body == deletedMessage {
		return "This post was deleted by its author."
	}
	return body
}
