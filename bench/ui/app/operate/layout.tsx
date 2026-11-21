import { createFileRoute } from "@tanstack/react-router";
import OperatePage from "./page";

export const Route = createFileRoute("/operate")({
	component: OperatePage,
});
