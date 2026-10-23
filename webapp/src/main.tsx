import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { DensityProvider, ThemeProvider } from "@flanksource/clicky-ui/hooks";
import "@flanksource/clicky-ui/styles.css";
import "./styles.css";
import App from "./App";

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
const client = new QueryClient();

createRoot(root).render(
  <StrictMode>
    <ThemeProvider><DensityProvider><QueryClientProvider client={client}>
      <App />
    </QueryClientProvider></DensityProvider></ThemeProvider>
  </StrictMode>,
);
