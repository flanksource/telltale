import "@testing-library/jest-dom/vitest";
import { cleanup, configure } from "@testing-library/react";
import { afterEach } from "vitest";

// findBy* waits 1s by default, which a page rendering behind a fetch can miss
// on a loaded CI runner running every suite at once.
configure({ asyncUtilTimeout: 5000 });

afterEach(cleanup);
