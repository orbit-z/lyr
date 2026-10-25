// Preloaded into the Astro server with `node --import` (bench/profile/run.sh).
// Each SIGUSR2 toggles a measurement window:
//   - PROFILE_MODE=cpu (default): a V8 CPU profile written to PROFILE_OUT (a
//     .cpuprofile file, which Chrome DevTools also opens), plus main-thread GC
//     pauses (perf_hooks "gc" entries), process CPU time and event-loop
//     utilization, written to PROFILE_OUT + ".json".
//   - PROFILE_MODE=promises: counts the promises and other async resources
//     created in the window (async_hooks), written to PROFILE_OUT + ".json".
//     It's kept out of the timed runs, because the hook slows everything down.
import { Session } from 'node:inspector/promises';
import { PerformanceObserver, performance } from 'node:perf_hooks';
import { createHook } from 'node:async_hooks';
import { writeFileSync } from 'node:fs';

const out = process.env.PROFILE_OUT || 'astro.cpuprofile';
const mode = process.env.PROFILE_MODE || 'cpu';
const interval = Number(process.env.PROFILE_INTERVAL_US || 200);

let active = null;

async function start() {
	const s = { t0: performance.now(), cpu0: process.cpuUsage(), elu0: performance.eventLoopUtilization() };
	if (mode === 'promises') {
		s.counts = {};
		s.hook = createHook({
			init(_id, type) {
				s.counts[type] = (s.counts[type] || 0) + 1;
			},
		});
		s.hook.enable();
	} else {
		s.gc = { count: 0, ms: 0, ms_by_kind: {} };
		s.obs = new PerformanceObserver((list) => {
			for (const e of list.getEntries()) {
				s.gc.count++;
				s.gc.ms += e.duration;
				const k = String(e.detail?.kind ?? e.kind);
				s.gc.ms_by_kind[k] = (s.gc.ms_by_kind[k] || 0) + e.duration;
			}
		});
		s.obs.observe({ entryTypes: ['gc'] });
		s.session = new Session();
		s.session.connect();
		await s.session.post('Profiler.enable');
		await s.session.post('Profiler.setSamplingInterval', { interval });
		await s.session.post('Profiler.start');
	}
	active = s;
}

async function stop() {
	const s = active;
	active = null;
	const wallMs = performance.now() - s.t0;
	const cpu = process.cpuUsage(s.cpu0);
	const stats = {
		mode,
		wall_ms: wallMs,
		process_cpu_ms: (cpu.user + cpu.system) / 1000,
		event_loop_utilization: performance.eventLoopUtilization(s.elu0).utilization,
	};
	if (mode === 'promises') {
		s.hook.disable();
		stats.async_resources = s.counts;
	} else {
		const { profile } = await s.session.post('Profiler.stop');
		s.session.disconnect();
		s.obs.disconnect();
		stats.gc = s.gc;
		writeFileSync(out, JSON.stringify(profile));
	}
	writeFileSync(out + '.json', JSON.stringify(stats, null, 2));
}

let queue = Promise.resolve();
process.on('SIGUSR2', () => {
	queue = queue.then(() => (active ? stop() : start())).catch((e) => console.error('profile hook:', e));
});
