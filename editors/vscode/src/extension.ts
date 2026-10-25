// The Orbit VS Code extension: starts `orbit lsp` for .orbit files and adds
// the Restart Language Server, Run orbit generate and Run orbit doctor
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
  TransportKind,
} from "vscode-languageclient/node";

const installURL = "https://github.com/bertrandkhe/orbit/blob/main/docs/editor-setup.md";
const installCommand = "go install github.com/bertrandkhe/orbit/cmd/orbit@latest";

let client: LanguageClient | undefined;
let output: vscode.OutputChannel;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  output = vscode.window.createOutputChannel("Orbit");
  context.subscriptions.push(
    output,
    vscode.commands.registerCommand("orbit.restartServer", () => restart()),
    vscode.commands.registerCommand("orbit.generate", () => runCommand("generate")),
    vscode.commands.registerCommand("orbit.doctor", () => runCommand("doctor")),
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("orbit.path") || e.affectsConfiguration("orbit.goplsPath")) {
        void restart();
      }
    }),
  );
  await start();
}

export async function deactivate(): Promise<void> {
  await stop();
}

// start launches `orbit lsp`, once for the window: the server finds the
// project root itself (the nearest go.mod next to a src/ directory).
async function start(): Promise<void> {
  // The server runs in the project root, so a global orbit hands over to
  // the version the project pins in go.mod (go tool orbit).
  const cwd = projectRoot() ?? firstFolder();
  const orbit = resolveOrbit(cwd);
  if (!orbit) {
    return;
  }
  const config = vscode.workspace.getConfiguration("orbit");
  const goplsPath = expandHome(config.get<string>("goplsPath", "").trim());
  const serverOptions: ServerOptions = {
    command: orbit.command,
    args: [...orbit.args, "lsp"],
    transport: TransportKind.stdio,
    options: cwd ? { cwd } : {},
  };
  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "orbit" }],
    initializationOptions: goplsPath ? { goplsPath } : {},
  };
  client = new LanguageClient("orbit", "Orbit Language Server", serverOptions, clientOptions);
  try {
    await client.start();
  } catch (err) {
    void vscode.window.showErrorMessage(`Orbit: couldn't start ${orbit.label} lsp: ${message(err)}`);
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

// orbitCommand is how to run the orbit CLI: a binary, or go tool orbit.
interface OrbitCommand {
  command: string;
  args: string[];
  label: string;
}

// resolveOrbit returns the orbit binary from orbit.path; with the default
// setting and no orbit on PATH, `go tool orbit` when the project's go.mod
// pins the CLI (like gopls's lookup, PATH first). Otherwise it shows an
// error pointing at the install instructions and returns undefined.
function resolveOrbit(root: string | undefined): OrbitCommand | undefined {
  const setting = vscode.workspace.getConfiguration("orbit").get<string>("path", "orbit").trim() || "orbit";
  const found = findBinary(setting);
  if (found) {
    return { command: found, args: [], label: found };
  }
  if (setting === "orbit" && root && pinsOrbit(root)) {
    const go = findBinary("go");
    if (go) {
      return { command: go, args: ["tool", "orbit"], label: "go tool orbit" };
    }
  }
  const what =
    setting === "orbit"
      ? "the orbit command wasn't found on your PATH"
      : `orbit.path is set to ${JSON.stringify(setting)}, which doesn't exist`;
  void vscode.window
    .showErrorMessage(
      `Orbit: ${what}. Install it with \`${installCommand}\`, or set orbit.path to the binary.`,
      "Install Instructions",
      "Open Settings",
    )
    .then((choice) => {
      if (choice === "Install Instructions") {
        void vscode.env.openExternal(vscode.Uri.parse(installURL));
      } else if (choice === "Open Settings") {
        void vscode.commands.executeCommand("workbench.action.openSettings", "orbit.path");
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

// pinsOrbit reports whether root's go.mod has the tool directive for the
// orbit CLI (orbit new writes it).
function pinsOrbit(root: string): boolean {
  try {
    const mod = fs.readFileSync(path.join(root, "go.mod"), "utf8");
    // `tool github.com/bertrandkhe/orbit/cmd/orbit`, or the path alone on
    // a line of a tool ( … ) block; require lines carry a version.
    return /^\s*(tool\s+)?github\.com\/bertrandkhe\/orbit\/cmd\/orbit\s*(\/\/.*)?$/m.test(mod);
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

// projectRoot returns the Orbit project (the nearest directory holding
// go.mod and src/) of the active .orbit file, else of the first workspace
// folder that is in one.
function projectRoot(): string | undefined {
  const doc = vscode.window.activeTextEditor?.document;
  if (doc && doc.uri.scheme === "file" && doc.languageId === "orbit") {
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

// runCommand runs `orbit <name>` in the project root, with its output in
// the Orbit output channel, and reports the result with a notification.
async function runCommand(name: "generate" | "doctor"): Promise<void> {
  const cwd = projectRoot();
  const orbit = resolveOrbit(cwd);
  if (!orbit) {
    return;
  }
  if (!cwd) {
    void vscode.window.showErrorMessage(
      `Orbit: no Orbit project found (a directory with go.mod and src/) for orbit ${name}. Open a .orbit file or a project folder.`,
    );
    return;
  }
  if (name === "doctor") {
    output.show(true);
  }
  output.appendLine(`$ orbit ${name}    (in ${cwd})`);
  const code = await vscode.window.withProgress(
    { location: vscode.ProgressLocation.Window, title: `orbit ${name}` },
    () =>
      new Promise<number | string>((resolve) => {
        const child = cp.spawn(orbit.command, [...orbit.args, name], { cwd });
        child.stdout.on("data", (b: Buffer) => output.append(b.toString()));
        child.stderr.on("data", (b: Buffer) => output.append(b.toString()));
        child.on("error", (err) => resolve(message(err)));
        child.on("close", (status, signal) => resolve(status ?? `killed by ${signal}`));
      }),
  );
  output.appendLine(code === 0 ? "" : `orbit ${name}: exit ${code}\n`);
  if (code === 0) {
    void vscode.window.showInformationMessage(
      name === "generate" ? "Orbit: orbit generate succeeded." : "Orbit: orbit doctor passed. Warnings, if any, are in the Orbit output.",
    );
    return;
  }
  const choice = await vscode.window.showErrorMessage(
    name === "generate"
      ? "Orbit: orbit generate failed. See the Orbit output for the errors."
      : "Orbit: orbit doctor found problems. See the Orbit output for the fixes.",
    "Show Output",
  );
  if (choice === "Show Output") {
    output.show();
  }
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
