// Shared page pieces (UI regroup contract). Import from "@/components/shell" or the file itself.
export { EmptyState, type EmptyStateAction, type EmptyStateProps } from "./emptyState";
export { EntityLink, entityHref, entityTarget, suiteIdOf, type EntityKind, type EntityLinkProps, type EntityTarget } from "./entityLink";
export { PageHeader, type Crumb, type PageHeaderProps } from "./pageHeader";
export { RouteTabs, resolveTab, useRouteTab, type RouteTab, type RouteTabsProps } from "./routeTabs";
export { StatusBadge, isDecision, isExperimentState, type StatusBadgeProps } from "./statusBadge";