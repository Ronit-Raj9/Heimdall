package tenancy

// Round 5 lane L8: "What if" on a drift monitor, a bounded simulation of
// the alarm rule. It spends no money but CPU, so members only.
func init() {
	OrgPolicy[key("POST", api+"/canaries/simulate")] = org(member)
}
