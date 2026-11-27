/**
 * The least role that may delete an organization's own benchmark or agent: the role the server's delete
 * routes allow (tenancy/policy_hw02.go DELETE /suites/{id}, tenancy/policy_fw13.go DELETE /agents/{id}).
 * Every Delete in the UI (the list rows and the Settings tab) offers itself from this role, and
 * tenancy/policy_iwint_test.go fails when the server's policy and this value part.
 */
export const DELETE_ROLE = "member" as const;