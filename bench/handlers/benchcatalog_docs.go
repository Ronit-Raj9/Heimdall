package handlers

// Catalog routes (lane L12).

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/benchmarks/catalog", Tag: "catalog",
			Summary: "Benchmark catalog: example projects, bundled suites and importable public benchmarks", Response: BenchCatalogList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/benchmarks/catalog/{id}/add", Tag: "catalog",
			Summary: "Add a catalog entry: copy an example or bundled suite (200), or start a catalog.import job (202)",
			Auth: AuthWrite, Request: BenchCatalogAdd{}, Response: BenchCatalogAdded{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/benchmarks/catalog/{id}/dataset", Tag: "catalog",
			Summary: "Upload an importer entry's dataset (multipart field \"file\": a zip or the importer's own file) into uploads/datasets/{id}; pass the returned from to add",
			Auth: AuthWrite, RawRequest: "multipart/form-data", Response: BenchCatalogDataset{}},
	)
}
