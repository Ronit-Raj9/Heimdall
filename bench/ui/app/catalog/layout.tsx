import { createFileRoute } from "@tanstack/react-router";
import CatalogPage from "./page";

export const Route = createFileRoute("/catalog")({
	component: CatalogPage,
});
