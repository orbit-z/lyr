# Session timing
- run 0 (cold: empty GOPLSCACHE): initialize 10ms, first completion request 301ms, process start→completion 312ms, items=54 (Post present: true) err=<nil>
- run 1 (warm gopls file cache): initialize 28ms, first completion request 74ms, process start→completion 102ms, items=58 (Post present: true) err=<nil>
- run 2 (warm gopls file cache): initialize 26ms, first completion request 73ms, process start→completion 99ms, items=58 (Post present: true) err=<nil>
- run 3 (warm gopls file cache): initialize 8ms, first completion request 74ms, process start→completion 82ms, items=58 (Post present: true) err=<nil>
