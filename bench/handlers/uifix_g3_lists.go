package handlers

import (
	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/schemas"
)

// AgentPagedList is GET /agents with limit, offset or q (UI fix wave, G3):
// one page of the agent list in its usual order. q matches the agent id and
// its file path, ignoring case.
type AgentPagedList struct {
	schemas.AgentList
	ListPage
}

func pagedAgents(p listPaging, l schemas.AgentList) AgentPagedList {
	rows, page := pageItems(p, l.Agents, func(a schemas.AgentSummary) []string { return []string{a.ID, a.Path} })
	return AgentPagedList{AgentList: schemas.AgentList{Agents: rows}, ListPage: page}
}

// CatalogPagedList is GET /catalog with limit, offset or q: the catalog with
// one page of its suites (q matches the suite id); probes, agents, metrics,
// gates and parts are whole. total counts the matching suites.
type CatalogPagedList struct {
	schemas.Catalog
	ListPage
}

func pagedCatalog(p listPaging, c schemas.Catalog) CatalogPagedList {
	rows, page := pageItems(p, c.Suites, func(s schemas.CatalogSuite) []string { return []string{s.ID, s.ID + "@" + s.Version} })
	c.Suites = rows
	return CatalogPagedList{Catalog: c, ListPage: page}
}

// ClusterPagedReport is GET /clusters with limit, offset or q: the report
// with one page of its listed clusters (q matches the signature, title and
// error text). total counts the matching listed clusters; hidden still
// counts the clusters below min.
type ClusterPagedReport struct {
	*clusters.Report
	ListPage
}

func pagedClusters(p listPaging, rep *clusters.Report) ClusterPagedReport {
	cp := *rep
	rows, page := pageItems(p, rep.Clusters, func(c clusters.Cluster) []string { return []string{c.Signature, c.Title, c.ErrorText} })
	cp.Clusters = rows
	return ClusterPagedReport{Report: &cp, ListPage: page}
}
