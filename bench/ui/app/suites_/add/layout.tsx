import { createFileRoute } from "@tanstack/react-router";
import AddSuitePage from "./page";

// UI regroup (U3): the Add a suite flow (the old /imports, plus bring your own).
export const Route = createFileRoute("/suites_/add")({
	component: AddSuitePage,
});