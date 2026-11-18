import { createFileRoute } from "@tanstack/react-router";
import SuitePage from "./page";

// UI regroup (U3): one suite's page with its tabs. "suites_" keeps it out of /suites, which renders
// no Outlet; the path is still /suites/$suiteId.
export const Route = createFileRoute("/suites_/$suiteId")({
	component: SuitePage,
});