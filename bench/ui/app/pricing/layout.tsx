import { createFileRoute } from "@tanstack/react-router";
import PricingPage from "./page";

// Public site (lane Y08): no sign-in, rendered in the public frame (lib/routing/publicPages.ts).
export const Route = createFileRoute("/pricing")({
	component: PricingPage,
});
