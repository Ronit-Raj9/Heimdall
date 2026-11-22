import { createFileRoute } from "@tanstack/react-router";
import FromTracesPage from "./page";

export const Route = createFileRoute("/agents_/from-traces")({
	component: FromTracesPage,
});
