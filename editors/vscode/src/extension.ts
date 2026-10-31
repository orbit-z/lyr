// The Lyr VS Code extension: starts `lyr lsp` for .lyr files (and
// Markdown files under src/, for their diagnostics) and adds
// the Restart Language Server, Run lyr generate and Run lyr doctor
// commands. Syntax highlighting and the language configuration are
// declarative (package.json, syntaxes/, language-configuration.json).

import * as cp from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import * as vscode from "vscode";
import {
  LanguageClient,
  type LanguageClientOptions,
  type ServerOptions,
} from "vscode-languageclient/node";

const installURL = "https://github.com/orbit-z/lyr/blob/main/docs/editor-setup.md";
const installCommand = "go install github.com/orbit-z/lyr/cmd/lyr@latest";

// markdownPattern matches the Markdown files Lyr compiles (PLAN.md §2.4:
// Astro's extensions), which are all under src/.
const markdownPattern = "**/src/**/*.{md,markdown,mdown,mkdn,mkd,mdwn}";

// isLyr reports whether a document is a .lyr file: the server's
// features other than diagnostics are for those only.
function isLyr(doc: vscode.TextDocument): boolean {
  return doc.languageId === "lyr";
}

let client: LanguageClient | undefined;
let output: vscode.OutputChannel;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  output = vscode.window.createOutputChannel("Lyr");
  context.subscriptions.push(
    output,
    vscode.commands.registerCommand("lyr.restartServer", () => restart()),
    vscode.commands.registerCommand("lyr.generate", () => runCommand("generate")),
    vscode.commands.registerCommand("lyr.doctor", () => runCommand("doctor")),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (
        e.affectsConfiguration("lyr.path") ||
        e.affectsConfiguration("lyr.goplsPath") ||
        e.affectsConfiguration("lyr.typescriptPath")
      ) {
        void restart();
      }
    }),
  );
  await start();
}

export async function deactivate(): Promise<void> {
  await stop();
}

// start launches `lyr lsp`, once for the window: the server finds the
// project root itself (the nearest go.mod next to a src/ directory).
async function start(): Promise<void> {
  // The server runs in the project root, so a global lyr hands over to
  // the version the project pins in go.mod (go tool lyr).
  const cwd = projectRoot() ?? firstFolder();
  const lyr = resolveLyr(cwd);
  if (!lyr) {
    return;
  }
  const config = vscode.workspace.getConfiguration("lyr");
  const goplsPath = expandHome(config.get<string>("goplsPath", "").trim());
  const typescriptPath = expandHome(config.get<string>("typescriptPath", "").trim());
  const initializationOptions: Record<string, string> = {};
  if (goplsPath) {
    initializationOptions.goplsPath = goplsPath;
  }
  if (typescriptPath) {
    initializationOptions.typescriptPath = typescriptPath;
  }
  const serverOptions: ServerOptions = {
    command: lyr.command,
    args: [...lyr.args, "lsp"],
    // No transport field: TransportKind.stdio would append --stdio to the
    // arguments. A plain command already talks over stdin/stdout.
    options: cwd ? { cwd } : {},
  };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [
      { scheme: "file", language: "lyr" },
      // Markdown pages and imports, for Lyr's diagnostics only: the
      // server ignores .md files that aren't compiled, and the middleware
      // below keeps every other request for VS Code's Markdown support.
      { scheme: "file", pattern: markdownPattern },
    ],
    initializationOptions,
    middleware: {
      provideHover: (doc, pos, token, next) => (isLyr(doc) ? next(doc, pos, token) : undefined),
      provideCompletionItem: (doc, pos, ctx, token, next) => (isLyr(doc) ? next(doc, pos, ctx, token) : undefined),
      provideDefinition: (doc, pos, token, next) => (isLyr(doc) ? next(doc, pos, token) : undefined),
      provideReferences: (doc, pos, opts, token, next) => (isLyr(doc) ? next(doc, pos, opts, token) : undefined),
      provideSignatureHelp: (doc, pos, ctx, token, next) => (isLyr(doc) ? next(doc, pos, ctx, token) : undefined),
      prepareRename: (doc, pos, token, next) => (isLyr(doc) ? next(doc, pos, token) : undefined),
      provideRenameEdits: (doc, pos, name, token, next) => (isLyr(doc) ? next(doc, pos, name, token) : undefined),
      // TypeScript features in <script> blocks.
      provideTypeDefinition: (doc, pos, token, next) => (isLyr(doc) ? next(doc, pos, token) : undefined),
      provideDocumentHighlights: (doc, pos, token, next) => (isLyr(doc) ? next(doc, pos, token) : undefined),
      provideCodeActions: (doc, range, ctx, token, next) => (isLyr(doc) ? next(doc, range, ctx, token) : undefined),
    },
  };
  client = new LanguageClient("lyr", "Lyr Language Server", serverOptions, clientOptions);
  try {
    await client.start();
  } catch (err) {
    void vscode.window.showErrorMessage(`Lyr: couldn't start ${lyr.label} lsp: ${message(err)}`);
  }
}

async function stop(): Promise<void> {
  const c = client;
  client = undefined;
  if (c) {
    try {
      await c.stop();
    } catch {
      // A server that failed to start can't be stopped cleanly.
    }
  }
}

async function restart(): Promise<void> {
  await stop();
  await start();
}

// lyrCommand is how to run the lyr CLI: a binary, or go tool lyr.
interface LyrCommand {
  command: string;
  args: string[];
  label: string;
}

// resolveLyr returns the lyr binary from lyr.path; with the default
// setting and no lyr on PATH, `go tool lyr` when the project's go.mod
// pins the CLI (like gopls's lookup, PATH first). Otherwise it shows an
// error pointing at the install instructions and returns undefined.
function resolveLyr(root: string | undefined): LyrCommand | undefined {
  const setting = vscode.workspace.getConfiguration("lyr").get<string>("path", "lyr").trim() || "lyr";
  const found = findBinary(setting);
  if (found) {
    return { command: found, args: [], label: found };
  }
  if (setting === "lyr" && root && pinsLyr(root)) {
    const go = findBinary("go");
    if (go) {
      return { command: go, args: ["tool", "lyr"], label: "go tool lyr" };
    }
  }
  const what =
    setting === "lyr"
      ? "the lyr command wasn't found on your PATH"
      : `lyr.path is set to ${JSON.stringify(setting)}, which doesn't exist`;
  void vscode.window
    .showErrorMessage(
      `Lyr: ${what}. Install it with \`${installCommand}\`, or set lyr.path to the binary.`,
      "Install Instructions",
      "Open Settings",
    )
    .then((choice) => {
      if (choice === "Install Instructions") {
        void vscode.env.openExternal(vscode.Uri.parse(installURL));
      } else if (choice === "Open Settings") {
        void vscode.commands.executeCommand("workbench.action.openSettings", "lyr.path");
      }
    });
  return undefined;
}

// findBinary resolves a bare name on PATH, ~/ against the home directory
// and a relative path against the first workspace folder.
function findBinary(setting: string): string | undefined {
  const p = expandHome(setting);
  if (!p.includes("/") && !p.includes("\\")) {
    const exts = process.platform === "win32" ? (process.env.PATHEXT ?? ".EXE").split(";") : [""];
    for (const dir of (process.env.PATH ?? "").split(path.delimiter)) {
      if (!dir) {
        continue;
      }
      for (const ext of exts) {
        const candidate = path.join(dir, p + ext);
        if (isExecutable(candidate)) {
          return candidate;
        }
      }
    }
    return undefined;
  }
  const folder = firstFolder();
  const abs = path.isAbsolute(p) || !folder ? p : path.join(folder, p);
  return isExecutable(abs) ? abs : undefined;
}

// pinsLyr reports whether root's go.mod has the tool directive for the
// lyr CLI (lyr new writes it).
function pinsLyr(root: string): boolean {
  try {
    const mod = fs.readFileSync(path.join(root, "go.mod"), "utf8");
    // `tool github.com/orbit-z/lyr/cmd/lyr`, or the path alone on
    // a line of a tool ( … ) block; require lines carry a version.
    return /^\s*(tool\s+)?github\.com\/orbit-z\/lyr\/cmd\/lyr\s*(\/\/.*)?$/m.test(mod);
  } catch {
    return false;
  }
}

function isExecutable(file: string): boolean {
  try {
    if (!fs.statSync(file).isFile()) {
      return false;
    }
    fs.accessSync(file, fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

function expandHome(p: string): string {
  return p === "~" || p.startsWith("~/") ? path.join(os.homedir(), p.slice(1)) : p;
}

function firstFolder(): string | undefined {
  return vscode.workspace.workspaceFolders?.find((f) => f.uri.scheme === "file")?.uri.fsPath;
}

// projectRoot returns the Lyr project (the nearest directory holding
// go.mod and src/) of the active .lyr file, else of the first workspace
// folder that is in one.
function projectRoot(): string | undefined {
  const doc = vscode.window.activeTextEditor?.document;
  if (doc && doc.uri.scheme === "file" && doc.languageId === "lyr") {
    const root = findRoot(path.dirname(doc.uri.fsPath));
    if (root) {
      return root;
    }
  }
  for (const f of vscode.workspace.workspaceFolders ?? []) {
    if (f.uri.scheme === "file") {
      const root = findRoot(f.uri.fsPath);
      if (root) {
        return root;
      }
    }
  }
  return undefined;
}

function findRoot(dir: string): string | undefined {
  for (let d = dir; ; ) {
    if (fs.existsSync(path.join(d, "go.mod")) && isDir(path.join(d, "src"))) {
      return d;
    }
    const parent = path.dirname(d);
    if (parent === d) {
      return undefined;
    }
    d = parent;
  }
}

function isDir(p: string): boolean {
  try {
    return fs.statSync(p).isDirectory();
  } catch {
    return false;
  }
}

// runCommand runs `lyr <name>` in the project root, with its output in
// the Lyr output channel, and reports the result with a notification.
async function runCommand(name: "generate" | "doctor"): Promise<void> {
  const cwd = projectRoot();
  const lyr = resolveLyr(cwd);
  if (!lyr) {
    return;
  }
  if (!cwd) {
    void vscode.window.showErrorMessage(
      `Lyr: no Lyr project found (a directory with go.mod and src/) for lyr ${name}. Open a .lyr file or a project folder.`,
    );
    return;
  }
  if (name === "doctor") {
    output.show(true);
  }
  output.appendLine(`$ lyr ${name}    (in ${cwd})`);
  const code = await vscode.window.withProgress(
    { location: vscode.ProgressLocation.Window, title: `lyr ${name}` },
    () =>
      new Promise<number | string>((resolve) => {
        const child = cp.spawn(lyr.command, [...lyr.args, name], { cwd });
        child.stdout.on("data", (b: Buffer) => output.append(b.toString()));
        child.stderr.on("data", (b: Buffer) => output.append(b.toString()));
        child.on("error", (err) => resolve(message(err)));
        child.on("close", (status, signal) => resolve(status ?? `killed by ${signal}`));
      }),
  );
  output.appendLine(code === 0 ? "" : `lyr ${name}: exit ${code}\n`);
  if (code === 0) {
    void vscode.window.showInformationMessage(
      name === "generate" ? "Lyr: lyr generate succeeded." : "Lyr: lyr doctor passed. Warnings, if any, are in the Lyr output.",
    );
    return;
  }
  const choice = await vscode.window.showErrorMessage(
    name === "generate"
      ? "Lyr: lyr generate failed. See the Lyr output for the errors."
      : "Lyr: lyr doctor found problems. See the Lyr output for the fixes.",
    "Show Output",
  );
  if (choice === "Show Output") {
    output.show();
  }
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
