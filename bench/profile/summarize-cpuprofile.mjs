// Summarizes a V8 .cpuprofile of the Astro server (bench/profile/run.sh)
// as Markdown tables: CPU share by category, self time by bundle region
// (the `//#region` comments rolldown leaves in dist/server, which name the
// original node_modules or src file), and top functions by self and
// inclusive time.
//
//   node summarize-cpuprofile.mjs astro.cpuprofile [load.json] [promises.json]
//
// Self time goes to the sampled (innermost) frame. A native frame without a
// script URL (a V8 builtin or C++ binding such as writev) is counted under
// the nearest caller that has one, except V8's own (garbage collector),
// (program) and (idle) nodes.
import { readFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const [profilePath, loadPath, promisesPath] = process.argv.slice(2);
const profile = JSON.parse(readFileSync(profilePath, 'utf8'));
const stats = existsSync(profilePath + '.json') ? JSON.parse(readFileSync(profilePath + '.json', 'utf8')) : null;
const load = loadPath && existsSync(loadPath) ? JSON.parse(readFileSync(loadPath, 'utf8')) : null;
const promises = promisesPath && existsSync(promisesPath) ? JSON.parse(readFileSync(promisesPath, 'utf8')) : null;

// --- regions -----------------------------------------------------------------
const regionCache = new Map();
function regionsOf(url) {
	if (regionCache.has(url)) return regionCache.get(url);
	let regions = null;
	if (url.startsWith('file://')) {
		try {
			const lines = readFileSync(fileURLToPath(url), 'utf8').split('\n');
			regions = [];
			lines.forEach((l, i) => {
				const m = /^\/\/#region (.*)$/.exec(l);
				if (m) regions.push({ line: i, name: m[1].replace(/^node_modules\//, '').replace(/^\0/, '') });
				else if (l.startsWith('//#endregion')) regions.push({ line: i, name: null });
			});
		} catch {
			regions = null;
		}
	}
	regionCache.set(url, regions);
	return regions;
}

function regionOf(frame) {
	const { url, lineNumber } = frame;
	if (!url) return '';
	const regions = regionsOf(url);
	const base = url.replace(/^file:\/\/.*\/dist\/server\//, 'dist/server/');
	if (!regions) return base;
	let name = null;
	for (const r of regions) {
		if (r.line > lineNumber) break;
		name = r.name;
	}
	return name ?? base;
}

// --- categories ---------------------------------------------------------------
// First match wins; tested against "region<TAB>function".
const categories = [
	['GC (main thread)', /\t\(garbage collector\)$/],
	['idle', /\t\(idle\)$/],
	['V8 (program, unattributed)', /\t\(program\)$/],
	['escaping and attributes', /\t(escapeHTML|entityFor|toAttributeString|classifyAttribute|addAttribute|escapeStyleText|spreadAttributes)$|runtime\/server\/escape\.js/],
	['component instances (Astro global, props, slots)', /\t(createAstro|createAstroPagePartial|registerIfPropagating)$|runtime\/server\/(astro-component|astro-global)\.js|render\/astro\/(instance|factory)\.js|render\/(component|slot)\.js|head-propagation/],
	['compiled .astro code (src/)', /^src\//],
	['render engine (template walk, string building, type checks)', /astro\/dist\/runtime\/server\//],
	['UTF-8 encoding of the HTML', /^node:internal\/encoding|\t(encode|encodeInto|encodeUtf8String)$/],
	['Astro request pipeline (routing, middleware, Response)', /^astro\/dist\//],
	['@astrojs/node adapter', /^@astrojs\/node\//],
	['Node HTTP, streams, sockets', /^node:|^undici|^(http|net|stream|_http)/],
	['other', /.*/],
];
const categoryOf = (key) => categories.find(([, re]) => re.test(key))[0];

// --- tree ---------------------------------------------------------------------
const nodes = new Map(profile.nodes.map((n) => [n.id, n]));
const parent = new Map();
for (const n of profile.nodes) for (const c of n.children ?? []) parent.set(c, n.id);

const special = new Set(['(garbage collector)', '(program)', '(idle)', '(root)']);
function attributed(id) {
	// The node self time is counted under: itself, or its nearest ancestor
	// with a script URL if it's a native frame.
	let n = nodes.get(id);
	const native = !n.callFrame.url && !special.has(n.callFrame.functionName);
	let cur = n;
	while (native && cur && !cur.callFrame.url) cur = nodes.get(parent.get(cur.id));
	return { node: cur ?? n, native: native ? n.callFrame.functionName || '(anonymous native)' : null };
}

const fnKey = (n) => {
	const f = n.callFrame;
	return `${f.functionName || '(anonymous)'}\t${regionOf(f)}${f.url ? ':' + (f.lineNumber + 1) : ''}`;
};

const counts = new Map();
for (const s of profile.samples) counts.set(s, (counts.get(s) || 0) + 1);

const selfByFn = new Map();
const selfByRegion = new Map();
const selfByCat = new Map();
const selfByNative = new Map();
const inclByFn = new Map();
let total = 0;
let idle = 0;
const add = (m, k, v) => m.set(k, (m.get(k) || 0) + v);

for (const [id, c] of counts) {
	const leaf = nodes.get(id);
	if (leaf.callFrame.functionName === '(idle)') {
		idle += c;
		continue;
	}
	total += c;
	const { node, native } = attributed(id);
	const region = regionOf(node.callFrame);
	const key = `${region}\t${node.callFrame.functionName}`;
	add(selfByCat, categoryOf(key), c);
	add(selfByRegion, region || node.callFrame.functionName, c);
	add(selfByFn, fnKey(node), c);
	if (native) add(selfByNative, native, c);
	const seen = new Set();
	for (let cur = leaf; cur; cur = nodes.get(parent.get(cur.id))) {
		const k = fnKey(cur);
		if (cur.callFrame.functionName === '(root)' || seen.has(k)) continue;
		seen.add(k);
		add(inclByFn, k, c);
	}
}

const pct = (v) => ((100 * v) / total).toFixed(1) + '%';
const top = (m, n) => [...m].sort((a, b) => b[1] - a[1]).slice(0, n);
const out = [];
const table = (head, rows) => {
	out.push(`| ${head.join(' | ')} |`, `|${head.map((_, i) => (i ? '---:' : '---')).join('|')}|`);
	for (const r of rows) out.push(`| ${r.join(' | ')} |`);
	out.push('');
};

out.push('## Astro', '');
if (load) {
	out.push(
		`- ${load.rps.toFixed(0)} req/s, p50 ${load.latency.p50_ms.toFixed(1)} ms, p99 ${load.latency.p99_ms.toFixed(1)} ms, ` +
			`${load.proc ? (load.proc.cpu_ms_per_1000 / 1000).toFixed(2) + ' ms CPU per request, ' + load.proc.cpu_cores.toFixed(2) + ' cores, ' : ''}` +
			`${load.errors} errors (the profiler is running)`,
	);
}
if (stats) {
	out.push(
		`- profile window ${(stats.wall_ms / 1000).toFixed(1)} s: process CPU ${(stats.process_cpu_ms / stats.wall_ms).toFixed(2)} cores ` +
			`(main thread plus V8's helper threads), event-loop utilization ${(stats.event_loop_utilization * 100).toFixed(0)}%`,
	);
	if (stats.gc) {
		out.push(
			`- GC pauses on the main thread (perf_hooks): ${stats.gc.count} GCs, ${stats.gc.ms.toFixed(0)} ms = ` +
				`${((100 * stats.gc.ms) / stats.wall_ms).toFixed(1)}% of the window ` +
				`(by kind, 1 = minor, 2 = major, 4 = incremental, 8 = weak callbacks: ` +
				`${Object.entries(stats.gc.ms_by_kind).map(([k, v]) => `${k}: ${v.toFixed(0)} ms`).join(', ')})`,
		);
		const helper = stats.process_cpu_ms - stats.wall_ms * stats.event_loop_utilization;
		out.push(`- CPU outside the main thread's busy time (GC helper threads, libuv): about ${helper.toFixed(0)} ms = ${((100 * helper) / stats.process_cpu_ms).toFixed(1)}% of process CPU`);
	}
}
if (load && stats) {
	const reqs = (load.rps * stats.wall_ms) / 1000;
	out.push(`- requests in the window: about ${reqs.toFixed(0)}; ${(stats.wall_ms / reqs).toFixed(3)} ms of main-thread time per request`);
}
if (promises) {
	const n = 100;
	const a = promises.async_resources;
	out.push(
		`- async resources per request (async_hooks, ${n} sequential requests): ` +
			Object.entries(a)
				.sort((x, y) => y[1] - x[1])
				.map(([k, v]) => `${k} ${(v / n).toFixed(1)}`)
				.join(', '),
	);
}
out.push(`- ${total} samples (plus ${idle} idle) at ${profile.samples.length && ((profile.endTime - profile.startTime) / profile.samples.length).toFixed(0)} µs`, '');

out.push('### Main-thread CPU by category (self time)', '');
table(['Category', 'Share'], top(selfByCat, 99).map(([k, v]) => [k, pct(v)]));

out.push('### Self time by bundle region (source file)', '');
table(['Region', 'Share'], top(selfByRegion, 25).map(([k, v]) => [k ? `\`${k}\`` : '(native)', pct(v)]));

out.push('### Top functions, self time', '');
table(['Function', 'Region:line', 'Share'], top(selfByFn, 30).map(([k, v]) => [...k.split('\t').map((s) => `\`${s}\``), pct(v)]));

out.push('### Native frames (V8 builtins, C++ bindings), counted under their caller above', '');
table(['Native', 'Share'], top(selfByNative, 12).map(([k, v]) => [`\`${k}\``, pct(v)]));

out.push('### Top functions, inclusive time (synchronous stacks only)', '');
table(['Function', 'Region:line', 'Share'], top(inclByFn, 30).map(([k, v]) => [...k.split('\t').map((s) => `\`${s}\``), pct(v)]));

console.log(out.join('\n'));
