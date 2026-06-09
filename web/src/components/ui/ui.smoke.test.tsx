import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Button } from "./button";
import { Card, CardContent, CardHeader, CardTitle } from "./card";
import { Badge } from "./badge";
import { Input } from "./input";
import { Label } from "./label";
import { Textarea } from "./textarea";
import { Skeleton } from "./skeleton";
import { Separator } from "./separator";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./tabs";

describe("ui primitives smoke", () => {
  it("Button renders a button with the default variant and safe type", () => {
    render(<Button>Click</Button>);
    const btn = screen.getByRole("button", { name: "Click" });
    expect(btn).toHaveAttribute("type", "button");
    expect(btn.className).toContain("bg-primary");
  });

  it("Button asChild renders the child element (anchor), not a button", () => {
    render(
      <Button asChild>
        <a href="/x">link</a>
      </Button>,
    );
    const link = screen.getByRole("link", { name: "link" });
    expect(link).toHaveAttribute("href", "/x");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("Card composes header/title/content", () => {
    render(
      <Card>
        <CardHeader>
          <CardTitle>Title</CardTitle>
        </CardHeader>
        <CardContent>Body</CardContent>
      </Card>,
    );
    expect(screen.getByText("Title")).toBeInTheDocument();
    expect(screen.getByText("Body")).toBeInTheDocument();
  });

  it("Badge applies its variant classes", () => {
    render(<Badge variant="destructive">bad</Badge>);
    const badge = screen.getByText("bad");
    expect(badge.className).toContain("bg-destructive");
  });

  it("Label is associated with an Input via htmlFor", () => {
    render(
      <div>
        <Label htmlFor="email">Email</Label>
        <Input id="email" placeholder="you@example.com" />
      </div>,
    );
    expect(screen.getByLabelText("Email")).toBe(
      screen.getByPlaceholderText("you@example.com"),
    );
  });

  it("Textarea renders as a textbox", () => {
    render(<Textarea defaultValue="hi" aria-label="notes" />);
    expect(screen.getByRole("textbox", { name: "notes" })).toHaveValue("hi");
  });

  it("Skeleton renders a pulse placeholder", () => {
    render(<Skeleton data-testid="sk" className="h-4 w-10" />);
    expect(screen.getByTestId("sk").className).toContain("animate-pulse");
  });

  it("Separator renders with a separator role when not decorative", () => {
    render(<Separator decorative={false} />);
    expect(screen.getByRole("separator")).toBeInTheDocument();
  });

  it("Tabs renders tab triggers and the active panel", () => {
    render(
      <Tabs defaultValue="a">
        <TabsList>
          <TabsTrigger value="a">A</TabsTrigger>
          <TabsTrigger value="b">B</TabsTrigger>
        </TabsList>
        <TabsContent value="a">Panel A</TabsContent>
        <TabsContent value="b">Panel B</TabsContent>
      </Tabs>,
    );
    expect(screen.getAllByRole("tab")).toHaveLength(2);
    expect(screen.getByRole("tabpanel")).toHaveTextContent("Panel A");
  });
});
