import { Card, CardContent } from "@/components/ui/card";

export interface PlaceholderScreenProps {
	title: string;
	description: string;
	/** data-testid of the section, "<screen>-empty-state". */
	testId: string;
}

/**
 * Heading plus empty-state message for a screen that is not built yet.
 * PLACEHOLDER: screens replace their views/*PlaceholderView.tsx with real views and stop using this.
 */
export function PlaceholderScreen({ title, description, testId }: PlaceholderScreenProps) {
	return (
		<section className="flex flex-col gap-4" data-testid={testId}>
			<h1 className="text-foreground text-xl font-semibold tracking-tight">{title}</h1>
			<Card className="border-dashed py-10 shadow-none">
				<CardContent>
					<p className="text-muted-foreground max-w-prose text-sm">{description}</p>
				</CardContent>
			</Card>
		</section>
	);
}