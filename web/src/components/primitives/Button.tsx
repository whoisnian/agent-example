import type { JSX } from "react";
import type { ButtonHTMLAttributes } from "react";

type Variant = "primary" | "ghost" | "danger";

const VARIANT: Record<Variant, string> = {
  primary: "bg-accent text-white hover:bg-accent",
  ghost: "bg-transparent text-text hover:bg-surface",
  danger: "bg-danger text-white hover:bg-danger",
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
}

export function Button({
  variant = "primary",
  className,
  type = "button",
  ...rest
}: ButtonProps): JSX.Element {
  return (
    <button
      type={type}
      className={[
        "inline-flex items-center justify-center rounded px-4 py-2 text-sm font-medium",
        "disabled:opacity-50",
        VARIANT[variant],
        className ?? "",
      ].join(" ")}
      {...rest}
    />
  );
}
