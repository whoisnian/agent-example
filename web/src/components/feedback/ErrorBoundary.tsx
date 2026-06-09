import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
  children: ReactNode;
  /** Optional error sink (e.g. Sentry forwarder). Placeholder in scaffold. */
  onError?: (err: Error, info: ErrorInfo) => void;
}

interface State {
  error: Error | null;
}

function reportError(err: Error, info: ErrorInfo): void {
  // Centralized handler. Logs the structured triple; can be extended later.
   
  console.error("[ErrorBoundary]", {
    message: err.message,
    stack: err.stack,
    route: typeof window !== "undefined" ? window.location.pathname : null,
    componentStack: info.componentStack,
  });
}

export class ErrorBoundary extends Component<Props, State> {
  public override state: State = { error: null };

  public static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  public override componentDidCatch(error: Error, info: ErrorInfo): void {
    reportError(error, info);
    this.props.onError?.(error, info);
  }

  private handleReload = (): void => {
    if (typeof window !== "undefined") window.location.reload();
  };

  public override render(): ReactNode {
    if (this.state.error) {
      return (
        <div
          role="alert"
          className="m-6 rounded border border-destructive bg-card p-6 text-foreground"
          data-testid="error-boundary-fallback"
        >
          <h2 className="mb-2 text-lg font-semibold text-destructive">Something went wrong.</h2>
          <p className="mb-4 text-sm text-muted-foreground">{this.state.error.message}</p>
          <button
            type="button"
            onClick={this.handleReload}
            className="rounded bg-primary px-4 py-2 text-sm text-primary-foreground hover:bg-primary/90"
          >
            Reload
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
