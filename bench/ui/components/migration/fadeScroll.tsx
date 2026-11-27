import { useScrollEdges } from "@/lib/hooks/useScrollEdges";
import { useRef, type ReactNode } from "react";

/**
 * A sideways scroll box for a wide table (M4-23, M4-30): the shared edge fade shows that more
 * columns sit past the edge. pinStart keeps the leading edge plain where row labels are sticky.
 */
export function FadeScroll({ children, pinStart = false }: { children: ReactNode; pinStart?: boolean }) {
	const ref = useRef<HTMLDivElement>(null);
	useScrollEdges(ref, { start: !pinStart });
	return (
		<div ref={ref} className="overflow-x-auto">
			{children}
		</div>
	);
}