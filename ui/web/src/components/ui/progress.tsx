import * as React from "react";
import { cn } from "../../lib/utils";

export type ProgressProps = React.HTMLAttributes<HTMLDivElement> & {
  value?: number;
};

export function Progress({ value = 0, className, ...props }: ProgressProps) {
  const bounded = Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0));

  return (
    <div
      className={cn("relative h-2 w-full overflow-hidden rounded-full bg-secondary", className)}
      role="progressbar"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(bounded)}
      {...props}
    >
      <div
        className="h-full rounded-full bg-primary transition-all"
        style={{ width: `${bounded}%` }}
      />
    </div>
  );
}
