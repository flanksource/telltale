import type { RouterAdapter } from "@flanksource/clicky-ui/rpc";

// A host serves the viewer under a prefix of its own by rewriting index.html's
// <base href="/">; the built assets are relative (vite base "./"), so they
// resolve against it, and the app's routes and API calls are derived from it.

/** The path the viewer is mounted at, without a trailing slash: "" at the root. */
export function basePath(href: string | null): string {
  if (href === null) return "";
  return new URL(href, "http://base.invalid").pathname.replace(/\/+$/, "");
}

/** The base of this document: its <base href>, or the root when it has none. */
export const BASE_PATH = basePath(typeof document === "undefined" ? null : document.querySelector("base[href]")?.getAttribute("href") ?? null);

/** path, an absolute path of the app, under base. */
export function withBase(path: string, base: string = BASE_PATH): string {
  return `${base}${path}`;
}

/** adapter as the app sees it under base: paths below the base in, above it out. */
export function basenameRouter(adapter: RouterAdapter, base: string = BASE_PATH): RouterAdapter {
  if (base === "") return adapter;
  const { pathname } = adapter;
  const below = pathname === base ? "/" : pathname.startsWith(`${base}/`) ? pathname.slice(base.length) : pathname;
  return {
    pathname: below,
    navigate: (to, opts) => adapter.navigate(to.startsWith("/") ? withBase(to, base) : to, opts),
    renderLink: props => adapter.renderLink({ ...props, to: props.to.startsWith("/") ? withBase(props.to, base) : props.to }),
  };
}
