import { createFileRoute } from "@tanstack/react-router";
import FromApiPage from "./page";

export const Route = createFileRoute("/suites_/from-api")({
	component: FromApiPage,
});