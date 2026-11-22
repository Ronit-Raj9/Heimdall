import { useSwitchOrgMutation } from "@/lib/store/apis/platformApi";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useCallback } from "react";

/**
 * Switches the session to another organization and reloads the current page's path (its query
 * string held ids of the old organization). Shared by the sidebar badge and the Account menu.
 */
export function useOrgSwitch(currentOrgId?: string) {
	const navigate = useNavigate();
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const [switchOrg, state] = useSwitchOrgMutation();
	const choose = useCallback(
		async (orgId: string) => {
			if (orgId === currentOrgId) return;
			try {
				await switchOrg({ org: orgId }).unwrap();
				void navigate({ href: pathname });
			} catch {
				// state.error renders the failure.
			}
		},
		[currentOrgId, navigate, pathname, switchOrg],
	);
	return { choose, isLoading: state.isLoading, error: state.error };
}