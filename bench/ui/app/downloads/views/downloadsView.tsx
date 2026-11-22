import { CopyBlock } from "@/components/platform/oneTimeSecret";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell";
import { SiteHeader } from "@/components/site/publicFrame";
import { useGetPublicDownloadsQuery } from "@/lib/store/apis/publicSiteApi";
import type { DownloadFile, DownloadsManifest } from "@/lib/types/site";
import "@/lib/types/siteI18n";
import { Download, FileCheck2, PackageOpen, Plug, Terminal, Workflow } from "lucide-react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";

/** Human size of a download: bytes as KB or MB, one decimal. */
export function formatSize(bytes: number): string {
	if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
	if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${bytes} B`;
}

/** The server's public address: what the manifest names, else this page's origin. */
export function baseURL(m: Pick<DownloadsManifest, "base_url">): string {
	if (m.base_url) return m.base_url.replace(/\/+$/, "");
	return typeof window === "undefined" ? "" : window.location.origin;
}

/** The shell that installs one command line build, checked against SHA256SUMS (docs/downloads.md). */
export function cliInstallCommand(base: string, asset: string): string {
	const sum = asset.includes("darwin") ? "shasum -a 256 -c -" : "sha256sum -c -";
	return [
		`curl -fsSLO ${base}/downloads/${asset}`,
		`curl -fsSL ${base}/downloads/SHA256SUMS | grep "  ${asset}$" | ${sum}`,
		`install -m 0755 ${asset} /usr/local/bin/heimdall && heimdall version`,
	].join("\n");
}

/** A GitHub Actions step that downloads the tool from this server and puts it on PATH. */
export function githubStep(base: string, asset: string): string {
	return [
		"- name: Download heimdall from your Heimdall server",
		"  run: |",
		`    curl -fsSLO ${base}/downloads/${asset}`,
		`    curl -fsSL ${base}/downloads/SHA256SUMS | grep "  ${asset}$" | sha256sum -c -`,
		`    install -m 0755 ${asset} "$RUNNER_TEMP/heimdall"`,
		'    echo "$RUNNER_TEMP" >> "$GITHUB_PATH"',
	].join("\n");
}

const OS_ORDER = ["linux", "darwin"];

/**
 * /downloads (lane Y08, audit G4-09): the command line tool, SDK packages, MCP and the pull
 * request gate, all served by this server and built at its commit (GET /public/downloads). No
 * GitHub or package registry. Every file shows its size and SHA-256.
 */
export default function DownloadsView() {
	const { t } = useTranslation("site");
	const downloads = useGetPublicDownloadsQuery();
	const m = downloads.data;
	return (
		<section className="flex min-w-0 flex-col gap-10" data-testid="downloads-view">
			<SiteHeader title={t("downloads.title")} subtitle={t("downloads.subtitle")}>
				{m?.available && (
					<p className="text-muted-foreground font-mono text-xs" data-testid="downloads-built-at">
						{t("downloads.builtAt", { commit: m.commit ? m.commit.slice(0, 12) : "?", version: m.version || "?" })}
					</p>
				)}
			</SiteHeader>
			{downloads.isLoading ? (
				<BlockSkeleton rows={5} testId="downloads-loading" />
			) : downloads.error || !m ? (
				<LoadError title={t("common.loadError")} error={downloads.error} onRetry={downloads.refetch} testId="downloads-error" />
			) : (
				<DownloadsBody m={m} />
			)}
		</section>
	);
}

function DownloadsBody({ m }: { m: DownloadsManifest }) {
	const { t } = useTranslation("site");
	const base = baseURL(m);
	const files = m.available ? m.files : [];
	const of = (kind: string) => files.filter((f) => f.kind === kind);
	const cli = of("cli").sort((a, b) => OS_ORDER.indexOf(a.os ?? "") - OS_ORDER.indexOf(b.os ?? "") || (a.arch ?? "").localeCompare(b.arch ?? ""));
	const wheel = of("python-wheel")[0];
	const sdist = of("python-sdist")[0];
	const ts = of("typescript")[0];
	const source = of("source")[0];
	const mcpURL = m.mcp_url || (base ? `${base}/mcp` : "");
	const gitlabURL = m.gitlab_url || (base ? `${base}/downloads/heimdall.gitlab-ci.yml` : "");
	const linux = cli.find((f) => f.os === "linux" && f.arch === "amd64") ?? cli[0];

	return (
		<div className="flex flex-col gap-6">
			{!m.available && (
				<EmptyState icon={PackageOpen} message={t("downloads.none")} testId="downloads-none" />
			)}
			{cli.length > 0 && (
				<Card icon={<Terminal />} title={t("downloads.cli.title")} body={t("downloads.cli.body")} testId="downloads-cli">
					<FileTable files={cli} base={base} label={(f) => `${t(`downloads.cli.${f.os === "darwin" ? "darwin" : "linux"}`)} ${f.arch ?? ""}`} />
					{linux && (
						<div className="flex flex-col gap-2">
							<p className="text-sm font-medium">{t("downloads.cli.install")}</p>
							<CopyBlock value={cliInstallCommand(base, linux.name)} label={t("downloads.cli.install")} testId="downloads-cli-install" className="whitespace-pre-wrap" />
						</div>
					)}
				</Card>
			)}
			{(wheel || sdist) && (
				<Card icon={<Download />} title={t("downloads.python.title")} body={t("downloads.python.body")} testId="downloads-python">
					<FileTable files={[wheel, sdist].filter((f): f is DownloadFile => !!f)} base={base} label={(f) => (f.kind === "python-sdist" ? t("downloads.python.sdist") : "Wheel")} />
					{wheel && <CopyBlock value={`pip install ${base}/downloads/${wheel.name}`} label={t("downloads.python.title")} testId="downloads-python-install" />}
				</Card>
			)}
			{ts && (
				<Card icon={<Download />} title={t("downloads.typescript.title")} body={t("downloads.typescript.body")} testId="downloads-typescript">
					<FileTable files={[ts]} base={base} label={() => "npm"} />
					<CopyBlock value={`npm install ${base}/downloads/${ts.name}`} label={t("downloads.typescript.title")} testId="downloads-typescript-install" />
				</Card>
			)}
			{mcpURL && (
				<Card icon={<Plug />} title={t("downloads.mcp.title")} body={t("downloads.mcp.body")} testId="downloads-mcp">
					<CopyBlock
						value={`claude mcp add --transport http heimdall ${mcpURL} --header "Authorization: Bearer $HEIMDALL_TOKEN"`}
						label={t("downloads.mcp.title")}
						testId="downloads-mcp-remote"
					/>
					<p className="text-muted-foreground text-sm">{t("downloads.mcp.local")}</p>
					<CopyBlock value={`HEIMDALL_TOKEN=hm_... heimdall mcp serve -url ${base}`} label={t("downloads.mcp.local")} testId="downloads-mcp-local" />
				</Card>
			)}
			{base && (
				<Card icon={<Workflow />} title={t("downloads.ci.title")} body={t("downloads.ci.body")} testId="downloads-ci">
					{gitlabURL && (
						<div className="flex flex-col gap-2">
							<p className="text-sm font-medium">{t("downloads.ci.gitlab")}</p>
							<CopyBlock value={`include:\n  - remote: ${gitlabURL}`} label={t("downloads.ci.gitlab")} testId="downloads-ci-gitlab" className="whitespace-pre-wrap" />
						</div>
					)}
					{linux && (
						<div className="flex flex-col gap-2">
							<p className="text-sm font-medium">{t("downloads.ci.github")}</p>
							<CopyBlock
								value={githubStep(base, linux.name)}
								label={t("downloads.ci.github")}
								testId="downloads-ci-github"
								className="whitespace-pre-wrap"
							/>
						</div>
					)}
				</Card>
			)}
			{source && (
				<Card icon={<PackageOpen />} title={t("downloads.source.title")} body={t("downloads.source.body")} testId="downloads-source">
					<FileTable files={[source]} base={base} label={() => "tar.gz"} />
				</Card>
			)}
			{m.available && (
				<div className="flex flex-wrap gap-4 text-sm" data-testid="downloads-sums">
					<a href="/downloads/SHA256SUMS" className="text-primary inline-flex items-center gap-1.5 hover:underline">
						<FileCheck2 className="size-4" aria-hidden="true" />
						{t("downloads.checksums")}
					</a>
					<a href="/downloads/manifest.json" className="text-primary inline-flex items-center gap-1.5 hover:underline">
						{t("downloads.manifest")}
					</a>
				</div>
			)}
		</div>
	);
}

function Card({ icon, title, body, children, testId }: { icon: ReactNode; title: string; body: string; children: ReactNode; testId: string }) {
	return (
		<section className="bg-card flex min-w-0 flex-col gap-4 rounded-2xl border p-6 shadow-(--shadow-card)" data-testid={testId}>
			<div className="flex items-start gap-3">
				<span className="bg-accent text-foreground flex size-9 shrink-0 items-center justify-center rounded-lg [&_svg]:size-4.5" aria-hidden="true">
					{icon}
				</span>
				<div className="flex min-w-0 flex-col gap-1">
					<h2 className="text-lg font-semibold">{title}</h2>
					<p className="text-muted-foreground text-sm leading-relaxed">{body}</p>
				</div>
			</div>
			{children}
		</section>
	);
}

function FileTable({ files, base, label }: { files: DownloadFile[]; base: string; label: (f: DownloadFile) => string }) {
	const { t } = useTranslation("site");
	return (
		<div className="overflow-x-auto rounded-lg border">
			<table className="w-full min-w-[36rem] text-sm">
				<thead className="text-muted-foreground bg-muted/40 text-left text-xs">
					<tr>
						<th className="px-3.5 py-2 font-medium">{t("downloads.cli.os")}</th>
						<th className="px-3.5 py-2 font-medium">{t("downloads.cli.size")}</th>
						<th className="px-3.5 py-2 font-medium">{t("downloads.cli.sha")}</th>
					</tr>
				</thead>
				<tbody className="divide-y">
					{files.map((f) => (
						<tr key={f.name} data-testid={`download-${f.name}`}>
							<td className="px-3.5 py-2.5">
								<a href={`${base}/downloads/${f.name}`} download className="text-primary inline-flex items-center gap-1.5 font-medium hover:underline">
									<Download className="size-3.5" aria-hidden="true" />
									{label(f)}
								</a>
								<div className="text-muted-foreground font-mono text-xs">{f.name}</div>
							</td>
							<td className="text-muted-foreground px-3.5 py-2.5 whitespace-nowrap tabular-nums">{formatSize(f.size)}</td>
							<td className="text-muted-foreground px-3.5 py-2.5 font-mono text-xs break-all">{f.sha256}</td>
						</tr>
					))}
				</tbody>
			</table>
		</div>
	);
}
