import { createFileRoute } from "@tanstack/react-router";
import McpTestPage from "./page";

export const Route = createFileRoute("/mcp-test")({
	component: McpTestPage,
});