package tenancy

// W4 feature 3 (D3 P7): the new model watch of an evaluation schedule. Any
// member of the org may read it; trying a model again is a member action,
// like running a schedule now.
func init() {
	OrgPolicy[key("GET", api+"/gateway/model-watch/{schedule}")] = org(viewer)
	OrgPolicy[key("POST", api+"/gateway/model-watch/{schedule}/retry")] = org(member)
}
