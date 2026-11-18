import { SessionContext, sessionFromMe } from "@/lib/session/sessionContext";
import { useAppDispatch } from "@/lib/store/hooks";
import { benchApi } from "@/lib/store/apis/benchApi";
import { useGetAuthConfigQuery, useGetMeQuery, useProbeDemoQuery } from "@/lib/store/apis/platformApi";
import { isUnauthorized, onUnauthorized } from "@/lib/store/apis/session";
import { useEffect, useMemo, useRef, type ReactNode } from "react";

/**
 * Asks GET /auth/me once and shares the answer as the session. A hosted visitor who is not signed
 * in gets the sign-in options (GET /auth/config) and one anonymous read (probeDemo): when it
 * answers, the server serves a public demo and the visitor reads it instead of the login page.
 * A hosted 401 on any other route while signed in re-asks /auth/me, so an ended session turns
 * anonymous and the shell sends the visitor to the login page.
 */
export function SessionProvider({ children }: { children: ReactNode }) {
	const me = useGetMeQuery();
	const anonymous = isUnauthorized(me.error);
	const config = useGetAuthConfigQuery(undefined, { skip: !anonymous });
	const probe = useProbeDemoQuery(undefined, { skip: !anonymous });
	const dispatch = useAppDispatch();
	const session = useMemo(
		() =>
			sessionFromMe({
				me: me.data,
				meError: me.error,
				meLoading: me.isLoading || (anonymous && (config.isLoading || probe.isLoading)),
				config: config.data,
				demoReadable: anonymous && probe.isSuccess,
			}),
		[me.data, me.error, me.isLoading, anonymous, config.data, config.isLoading, probe.isLoading, probe.isSuccess],
	);

	const signedIn = useRef(session.signedIn);
	signedIn.current = session.signedIn;
	useEffect(() => {
		onUnauthorized(() => {
			if (signedIn.current) dispatch(benchApi.util.invalidateTags(["AuthMe"]));
		});
		return () => onUnauthorized(null);
	}, [dispatch]);

	return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}