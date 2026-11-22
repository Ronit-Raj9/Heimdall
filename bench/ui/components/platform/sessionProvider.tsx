import { meIsAnonymous, SessionContext, sessionFromMe } from "@/lib/session/sessionContext";
import { useAppDispatch } from "@/lib/store/hooks";
import { benchApi } from "@/lib/store/apis/benchApi";
import { useGetAuthConfigQuery, useGetMeQuery } from "@/lib/store/apis/platformApi";
import { onUnauthorized, ORG_SWITCH_KEY } from "@/lib/store/apis/session";
import { useEffect, useMemo, useRef, type ReactNode } from "react";

/**
 * Asks GET /auth/me once and shares the answer as the session. A hosted visitor who is not signed
 * in gets the sign-in options (GET /auth/config) and the login page: hosted installs require sign-in
 * and serve no public demo, so no anonymous read is ever tried.
 * A hosted 401 on any other route while signed in re-asks /auth/me, so an ended session turns
 * anonymous and the shell sends the visitor to the login page. An org switch in another tab (the
 * storage event of ORG_SWITCH_KEY) re-asks /auth/me too; a changed org then drops the whole cache.
 */
export function SessionProvider({ children }: { children: ReactNode }) {
	const me = useGetMeQuery();
	// A visitor: 401 (an ended session) or 200 with kind anonymous (no credential at all).
	const anonymous = meIsAnonymous(me.data, me.error);
	const config = useGetAuthConfigQuery(undefined, { skip: !anonymous });
	const dispatch = useAppDispatch();
	const session = useMemo(
		() =>
			sessionFromMe({
				me: me.data,
				meError: me.error,
				meLoading: me.isLoading || (anonymous && config.isLoading),
				config: config.data,
			}),
		[me.data, me.error, me.isLoading, anonymous, config.data, config.isLoading],
	);

	const signedIn = useRef(session.signedIn);
	signedIn.current = session.signedIn;
	useEffect(() => {
		onUnauthorized(() => {
			if (signedIn.current) dispatch(benchApi.util.invalidateTags(["AuthMe"]));
		});
		return () => onUnauthorized(null);
	}, [dispatch]);

	useEffect(() => {
		const onStorage = (event: StorageEvent) => {
			if (event.key === ORG_SWITCH_KEY) dispatch(benchApi.util.invalidateTags(["AuthMe"]));
		};
		window.addEventListener("storage", onStorage);
		return () => window.removeEventListener("storage", onStorage);
	}, [dispatch]);

	return <SessionContext.Provider value={session}>{children}</SessionContext.Provider>;
}