import * as React from "react";
import { cn } from "../../lib/utils";

export function TabsList({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        "inline-flex h-8 items-center rounded-md border border-border bg-secondary/70 p-0.5 text-muted-foreground",
        className,
      )}
      role="tablist"
      {...props}
    />
  );
}

export type TabsTriggerProps = React.ButtonHTMLAttributes<HTMLButtonElement> & {
  active?: boolean;
};

export function TabsTrigger({ active, className, ...props }: TabsTriggerProps) {
  return (
    <button
      className={cn(
        "inline-flex h-7 items-center justify-center rounded-sm px-3 text-xs font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
        active ? "bg-background text-foreground shadow-xs" : "text-muted-foreground hover:bg-background/60 hover:text-foreground",
        className,
      )}
      role="tab"
      aria-selected={active}
      {...props}
    />
  );
}
