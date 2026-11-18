import { createFileRoute } from "@tanstack/react-router";
import InboxPage from "./page";

export const Route = createFileRoute("/inbox")({
	component: InboxPage,
});