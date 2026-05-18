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
          className="m-6 rounded border border-danger bg-surface p-6 text-text"
          data-testid="error-boundary-fallback"
        >
          <h2 className="mb-2 text-lg font-semibold text-danger">Something went wrong.</h2>
          <p className="mb-4 text-sm text-text-muted">{this.state.error.message}</p>
          <button
            type="button"
            onClick={this.handleReload}
            className="rounded bg-accent px-4 py-2 text-sm text-white hover:bg-accent"
          >
            Reload
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
