import { useGetAuthConfigQuery } from "@/lib/store/apis/platformApi";
import type { CaptchaConfig } from "@/lib/types/accountSafety";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

// Account safety (L22): the optional hCaptcha widget on self-serve sign-up. The server turns it on
// with HEIMDALL_CAPTCHA_SECRET and tells the page the public site key in GET /auth/config.

const SCRIPT_SRC = "https://js.hcaptcha.com/1/api.js?render=explicit";

interface HCaptchaApi {
	render(el: HTMLElement, opts: Record<string, unknown>): string;
	remove?(id: string): void;
}

declare global {
	interface Window {
		hcaptcha?: HCaptchaApi;
	}
}

let loading: Promise<HCaptchaApi> | null = null;

function loadScript(): Promise<HCaptchaApi> {
	if (window.hcaptcha) return Promise.resolve(window.hcaptcha);
	if (loading) return loading;
	loading = new Promise((resolve, reject) => {
		const el = document.createElement("script");
		el.src = SCRIPT_SRC;
		el.async = true;
		el.onload = () => (window.hcaptcha ? resolve(window.hcaptcha) : reject(new Error("hcaptcha missing")));
		el.onerror = () => {
			loading = null;
			reject(new Error("hcaptcha script failed"));
		};
		document.head.appendChild(el);
	});
	return loading;
}

/** Whether sign-up needs a captcha, and the site key to render it with. */
export function useCaptchaConfig(): CaptchaConfig {
	const config = useGetAuthConfigQuery();
	const data = config.data as (CaptchaConfig & object) | undefined;
	return { captcha: Boolean(data?.captcha), captcha_site_key: data?.captcha_site_key };
}

/** The hCaptcha widget; onToken gets the response token, or "" when it expires. */
export function HCaptcha({ siteKey, onToken, testId = "hcaptcha" }: { siteKey?: string; onToken: (token: string) => void; testId?: string }) {
	const { t } = useTranslation("accountSafety");
	const box = useRef<HTMLDivElement>(null);
	const [failed, setFailed] = useState(false);
	const tokenRef = useRef(onToken);
	tokenRef.current = onToken;

	useEffect(() => {
		if (!siteKey || !box.current) return;
		let id: string | undefined;
		let cancelled = false;
		loadScript().then(
			(api) => {
				if (cancelled || !box.current) return;
				id = api.render(box.current, {
					sitekey: siteKey,
					callback: (token: string) => tokenRef.current(token),
					"expired-callback": () => tokenRef.current(""),
					"error-callback": () => tokenRef.current(""),
				});
			},
			() => !cancelled && setFailed(true),
		);
		return () => {
			cancelled = true;
			if (id && window.hcaptcha?.remove) window.hcaptcha.remove(id);
		};
	}, [siteKey]);

	if (!siteKey) {
		return (
			<p role="alert" className="text-chart-error-ink text-xs" data-testid={`${testId}-no-key`}>
				{t("captcha.noSiteKey")}
			</p>
		);
	}
	return (
		<div className="flex flex-col gap-1.5" data-testid={testId}>
			<span className="text-[13px] font-medium">{t("captcha.label")}</span>
			<div ref={box} />
			{failed && (
				<p role="alert" className="text-chart-error-ink text-xs" data-testid={`${testId}-error`}>
					{t("captcha.loadError")}
				</p>
			)}
		</div>
	);
}
