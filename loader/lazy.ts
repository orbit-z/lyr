// Entry of the lazy loader script (loaderLazyJS). See src/lazy.ts.
import { lazy } from "./src/lazy";

lazy((url) => import(url));
