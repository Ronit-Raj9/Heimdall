package handlers

// Lane I28 (W4-41): first divergence of two runs and behaviour checks.
func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/pairs/{id}/divergence", Tag: "pairs",
			Summary:  "First point where the two runs of a pair did something different, and what each did and spent after it",
			Response: PairDivergence{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/divergence/{other}", Tag: "runs",
			Summary:  "First point where two runs of the same task did something different; 409 when the tasks differ",
			Response: PairDivergence{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/behaviour-checks", Tag: "behaviour",
			Summary:  "The built-in behaviour checks, the organization's own checks and the step word lists",
			Response: BehaviourChecks{}},
		RouteDoc{Method: "PUT", Path: APIPrefix + "/behaviour-checks", Tag: "behaviour",
			Summary: "Replace the organization's behaviour checks (admin); 422 lists each check that is not valid",
			Request: BehaviourChecksRequest{}, Response: BehaviourChecks{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/behaviour", Tag: "runs",
			Summary:  "Every behaviour check on one run, with the steps that matched; checks never change a score",
			Response: RunBehaviour{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/experiments/{id}/behaviour", Tag: "experiments",
			Summary:  "Every behaviour check over an experiment's runs, per arm, split by the checked result",
			Response: ExperimentBehaviour{}},
	)
}
