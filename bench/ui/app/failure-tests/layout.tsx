import { createFileRoute } from "@tanstack/react-router";
import FailureTestsPage from "./page";

export const Route = createFileRoute("/failure-tests")({
	component: FailureTestsPage,
});
