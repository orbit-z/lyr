// Summarizes a Go CPU profile of the Orbit server (bench/profile/run.sh) as
// Markdown tables, from the output of `go tool pprof -traces`: CPU share by
// category (each sample goes to the first category whose rule matches its
// stack), the page render's own breakdown by innermost frame, and the top
// functions by self time.
//
//   go tool pprof -traces orbit-server orbit.pprof > traces.txt
//   node summarize-pprof.mjs traces.txt [load.json]
import { readFileSync, existsSync } from 'node:fs';

const [tracesPath, loadPath] = process.argv.slice(2);
const text = readFileSync(tracesPath, 'utf8');
const load = loadPath && existsSync(loadPath) ? JSON.parse(readFileSync(loadPath, 'utf8')) : null;

const unit = { ns: 1e-6, us: 1e-3, µs: 1e-3, ms: 1, s: 1000 };
const samples = []; // { ms, stack: [leaf, ..., root] }
let header = [];
for (const block of text.split(/^-+\+-+$/m)) {
	const lines = block.split('\n').filter((l) => l.trim());
	if (!lines.length) continue;
	const m = /^\s*([\d.]+)(ns|us|µs|ms|s)\s+(.*)$/.exec(lines[0]);
	if (!m) {
		header = lines;
		continue;
	}
	const stack = [m[3], ...lines.slice(1)].map((l) => l.trim().replace(/ \(inline\)$/, '')).filter((l) => !l.startsWith('bytes:'));
	samples.push({ ms: Number(m[1]) * unit[m[2]], stack });
}

const has = (stack, re) => stack.some((f) => re.test(f));
const categories = [
	['GC (background marking, sweeping, assists)', (s) => has(s, /^runtime\.(gcBgMarkWorker|bgsweep|bgscavenge|gcAssistAlloc|gcStart|gcMarkDone|gcMarkTermination|gcDrain)/)],
	['allocation (mallocgc)', (s) => has(s, /^runtime\.mallocgc/)],
	['syscalls (socket read/write, epoll)', (s) => has(s, /^(syscall\.|internal\/runtime\/syscall\.|runtime\.netpoll|internal\/poll\.)/)],
	['page render (generated code, writer, escaping)', (s) => has(s, /\/orbit_gen\//)],
	['Orbit serving (routing, context, response)', (s) => has(s, /^github\.com\/bertrandkhe\/orbit\./)],
	['net/http (request parsing, headers, connection)', (s) => has(s, /^(net\/http|net\/textproto|bufio|net\.)/)],
	['Go scheduler and other runtime', (s) => has(s, /^runtime\./)],
	['other', () => true],
];

const add = (m, k, v) => m.set(k, (m.get(k) || 0) + v);
const byCat = new Map();
const renderByLeaf = new Map();
const selfByFn = new Map();
let total = 0;
for (const { ms, stack } of samples) {
	total += ms;
	const cat = categories.find(([, f]) => f(stack))[0];
	add(byCat, cat, ms);
	add(selfByFn, stack[0], ms);
	if (cat.startsWith('page render')) {
		const leaf = stack[0];
		let k;
		if (/^(runtime\.memmove|bytes\.)/.test(leaf)) k = 'buffer writes (bytes.Buffer, memmove of static HTML and text)';
		else if (/orbit\.(escape|firstEsc|.*[Ee]sc)/.test(leaf)) k = 'escaping (scan + escape)';
		else if (/^(internal\/)?strconv\./.test(leaf)) k = 'number formatting (strconv)';
		else if (/^runtime\./.test(leaf)) k = 'runtime (write barriers, stack growth, GC bookkeeping)';
		else if (/^github\.com\/bertrandkhe\/orbit\./.test(leaf)) k = 'other Orbit runtime (writer, slots, URL check)';
		else if (/\/orbit_gen\//.test(leaf)) k = 'generated component code';
		else k = 'other (' + leaf + ')';
		add(renderByLeaf, k, ms);
	}
}

const pct = (v, t = total) => ((100 * v) / t).toFixed(1) + '%';
const top = (m, n) => [...m].sort((a, b) => b[1] - a[1]).slice(0, n);
const out = [];
const table = (head, rows) => {
	out.push(`| ${head.join(' | ')} |`, `|${head.map((_, i) => (i ? '---:' : '---')).join('|')}|`);
	for (const r of rows) out.push(`| ${r.join(' | ')} |`);
	out.push('');
};

out.push('## Orbit', '');
if (load) {
	out.push(
		`- ${load.rps.toFixed(0)} req/s, p50 ${load.latency.p50_ms.toFixed(1)} ms, p99 ${load.latency.p99_ms.toFixed(1)} ms, ` +
			`${load.proc ? (load.proc.cpu_ms_per_1000 / 1000).toFixed(3) + ' ms CPU per request, ' + load.proc.cpu_cores.toFixed(2) + ' cores, ' : ''}` +
			`${load.errors} errors (the profiler is running)`,
	);
}
const dur = header.find((l) => l.startsWith('Duration:'));
if (dur) out.push(`- ${dur.trim()}`);
out.push(`- ${total.toFixed(0)} ms of samples`, '');

out.push('### CPU by category (first matching rule over the whole stack)', '');
table(['Category', 'Share'], top(byCat, 99).map(([k, v]) => [k, pct(v)]));

const renderTotal = byCat.get('page render (generated code, writer, escaping)') || 0;
out.push('### Inside the page render, by innermost frame', '');
table(['Where', 'Share of render', 'Share of all'], top(renderByLeaf, 12).map(([k, v]) => [k, pct(v, renderTotal), pct(v)]));

out.push('### Top functions, self time', '');
table(['Function', 'Share'], top(selfByFn, 25).map(([k, v]) => [`\`${k}\``, pct(v)]));

console.log(out.join('\n'));
