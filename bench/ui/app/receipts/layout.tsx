import { createFileRoute } from "@tanstack/react-router";
import ReceiptsPage from "./page";

export const Route = createFileRoute("/receipts")({
	component: ReceiptsPage,
});