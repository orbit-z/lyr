// Entry of the loader script (/_lyr/loader/loader-<hash>.js), built as a
// classic script (IIFE). See src/loader.ts.
import { start } from "./src/loader";

start(document.currentScript, (url) => import(url));
