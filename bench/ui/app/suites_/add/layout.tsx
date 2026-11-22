import { createFileRoute } from "@tanstack/react-router";
import AddBenchmarkPage from "./page";

// Add benchmark (lane Z20): one front door for any input. The importer list
// stays on the Import tab of Benchmarks (/suites?tab=import).
export const Route = createFileRoute("/suites_/add")({
	component: AddBenchmarkPage,
});
