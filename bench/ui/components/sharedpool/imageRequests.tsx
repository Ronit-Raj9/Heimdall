import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { getBenchErrorMessage } from "@/lib/store/apis/benchApi";
import { useGetPoolStatusQuery, useListImageRequestsQuery, useRequestImageMutation } from "@/lib/store/apis/sharedpoolApi";
import type { ImageRequest, ImageRequestStatus } from "@/lib/types/sharedpool";
import { isPinnedImage } from "@/lib/types/sharedpool";
import { Container } from "lucide-react";
import { useState } from "react";

/**
 * Your Docker images on shared workers (lane X10, J6). Shared workers run only the server's built-in
 * images; an org's own image needs a server admin's approval. This card lists the org's requests with
 * their status and the admin's note, and sends a new one (POST /images). AgentImageApproval is the
 * same status for one agent's image, shown on the agent page.
 */

const MIB = 1 << 20;

/** Images the shared workers run without approval: the server's built-in ones. */
export function isBuiltinImage(ref: string): boolean {
	return ref.trim().startsWith("bench/");
}

/** The newest request for an image (requests come newest first). */
export function requestFor(requests: readonly ImageRequest[] | undefined, image: string): ImageRequest | undefined {
	const ref = image.trim();
	return (requests ?? []).find((r) => r.image === ref);
}

const STATUS_TEXT: Record<ImageRequestStatus, string> = { pending: "Waiting for approval", approved: "Approved", rejected: "Not approved" };
const STATUS_VARIANT: Record<ImageRequestStatus, "warning" | "success" | "destructive"> = { pending: "warning", approved: "success", rejected: "destructive" };

export function ImageStatusBadge({ status }: { status: ImageRequestStatus }) {
	return (
		<Badge variant={STATUS_VARIANT[status] ?? "neutral"} data-testid={`image-status-${status}`}>
			{STATUS_TEXT[status] ?? status}
		</Badge>
	);
}

/** Why a request cannot be sent yet, or "". */
export function requestProblem(image: string, sizeMiB: string): string {
	if (!image.trim()) return "Enter the image reference.";
	if (!isPinnedImage(image)) return "Pin the image by digest: name@sha256:<64 hex characters>. Find it with docker inspect --format '{{index .RepoDigests 0}}' <image>.";
	const n = Number(sizeMiB);
	if (!sizeMiB.trim() || !Number.isFinite(n) || n <= 0) return "Enter the image size in MiB (docker image inspect --format '{{.Size}}' <image>, divided by 1048576).";
	return "";
}

function RequestForm({ initialImage = "", onDone }: { initialImage?: string; onDone?(): void }) {
	const [image, setImage] = useState(initialImage);
	const [size, setSize] = useState("");
	const [note, setNote] = useState("");
	const [touched, setTouched] = useState(false);
	const [send, state] = useRequestImageMutation();
	const problem = requestProblem(image, size);
	const submit = async () => {
		setTouched(true);
		if (problem) return;
		const res = await send({ image: image.trim(), size_bytes: Math.round(Number(size) * MIB), ...(note.trim() ? { note: note.trim() } : {}) });
		if ("data" in res && res.data) {
			setImage("");
			setSize("");
			setNote("");
			setTouched(false);
			onDone?.();
		}
	};
	return (
		<form
			className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_8rem]"
			onSubmit={(e) => {
				e.preventDefault();
				void submit();
			}}
			data-testid="image-request-form"
		>
			<label className="flex min-w-0 flex-col gap-1 text-sm">
				<span className="font-medium">Image</span>
				<Input className="font-mono" value={image} placeholder="ghcr.io/your-org/agent@sha256:..." onChange={(e) => setImage(e.target.value)} data-testid="image-request-ref" />
			</label>
			<label className="flex flex-col gap-1 text-sm">
				<span className="font-medium">Size (MiB)</span>
				<Input inputMode="numeric" value={size} placeholder="850" onChange={(e) => setSize(e.target.value)} data-testid="image-request-size" />
			</label>
			<label className="flex flex-col gap-1 text-sm sm:col-span-2">
				<span className="font-medium">Note for the admin (optional)</span>
				<Input value={note} maxLength={500} placeholder="Our support agent, built from main" onChange={(e) => setNote(e.target.value)} />
			</label>
			{touched && problem && (
				<p className="text-destructive text-xs sm:col-span-2" data-testid="image-request-problem">
					{problem}
				</p>
			)}
			{state.error && (
				<p role="alert" className="text-destructive text-xs sm:col-span-2">
					{getBenchErrorMessage(state.error)}
				</p>
			)}
			<Button type="submit" className="w-fit" disabled={state.isLoading} data-testid="image-request-submit">
				{state.isLoading ? "Sending..." : "Request approval"}
			</Button>
		</form>
	);
}

/** The org's image requests and the request form (Workers page). Hidden when the server has no shared workers. */
export function ImageRequestsCard({ initialImage }: { initialImage?: string }) {
	const pool = useGetPoolStatusQuery();
	const list = useListImageRequestsQuery();
	if (pool.isLoading || !pool.data?.shared_pool_configured) return null;
	const requests = list.data?.images ?? [];
	const allowed = pool.data.limits?.allow_custom_images;
	return (
		<section className="bg-card flex min-w-0 flex-col gap-4 rounded-xl border p-5" id="images" data-testid="image-requests">
			<div className="flex items-start gap-3">
				<span className="bg-muted flex size-9 shrink-0 items-center justify-center rounded-lg">
					<Container className="size-4" aria-hidden="true" />
				</span>
				<div className="min-w-0">
					<h2 className="text-sm font-semibold">Your Docker images on shared workers</h2>
					<p className="text-muted-foreground text-xs">
						Shared workers run Heimdall's built-in images. To run your own image there, request approval from the server admin, or connect your own worker.
					</p>
				</div>
			</div>
			{pool.data?.image_approval && (
				<p className="text-muted-foreground text-xs" data-testid="image-approval-mode">
					{pool.data.image_approval}
				</p>
			)}
			{allowed === false && (
				<p className="border-chart-warning/40 bg-chart-warning/10 rounded-md border px-3 py-2 text-xs" data-testid="image-requests-off">
					Your org cannot run custom images on shared workers yet. You can still send a request: the admin turns this on when approving it.
				</p>
			)}
			{requests.length > 0 && (
				<ul className="flex flex-col divide-y rounded-md border" data-testid="image-requests-list">
					{requests.map((r) => (
						<li key={r.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 text-sm" data-testid={`image-request-${r.id}`}>
							<span className="min-w-0 flex-1 truncate font-mono text-xs" title={r.image}>
								{r.image}
							</span>
							<ImageStatusBadge status={r.status} />
							{r.decision && r.approval !== "automatic" && <span className="text-muted-foreground w-full text-xs">Admin note: {r.decision}</span>}
							{r.next_step && (
								<span className="text-muted-foreground w-full text-xs" data-testid={`image-next-step-${r.id}`}>
									{r.next_step}
								</span>
							)}
						</li>
					))}
				</ul>
			)}
			<RequestForm initialImage={initialImage} />
		</section>
	);
}

/** One agent's image on shared workers: built in, approved, waiting, not approved, or a link to request it. */
export function AgentImageApproval({ image }: { image: string }) {
	const pool = useGetPoolStatusQuery();
	const list = useListImageRequestsQuery();
	if (!image || isBuiltinImage(image) || !pool.data?.shared_pool_configured) return null;
	const req = requestFor(list.data?.images, image);
	return (
		<div className="flex flex-wrap items-center gap-2 text-xs" data-testid="agent-image-approval">
			<span className="text-muted-foreground">Shared workers:</span>
			{req ? (
				<>
					<ImageStatusBadge status={req.status} />
					{req.status === "rejected" && req.decision && <span className="text-muted-foreground">{req.decision}</span>}
					{req.next_step && <span className="text-muted-foreground" data-testid="agent-image-next-step">{req.next_step}</span>}
				</>
			) : (
				<>
					<Badge variant="neutral">Needs approval</Badge>
					<a className="text-primary hover:underline" href={`/workers?image=${encodeURIComponent(image)}#images`} data-testid="agent-image-request-link">
						Request approval
					</a>
					<span className="text-muted-foreground">or run it on your own worker.</span>
				</>
			)}
		</div>
	);
}
