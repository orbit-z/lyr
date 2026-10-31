// Package cache provides in-process caches for Lyr sites: MemoryProvider
// (made with NewMemory), a route cache provider for lyr.WithCache, and
// Store (made with NewStore), a general cache for data and rendered
// fragments.
//
// Both use one engine: a map split into shards, bounded by an entry count
// (MaxEntries, 1000 by default) and optionally by bytes (MaxBytes). When a
// shard is full, it evicts with the CLOCK algorithm, an approximation of
// least recently used: a read marks an entry, and eviction gives a marked
// entry a second chance. The bounds are checked per shard, so they're
// approximate. Expiry is lazy: an expired entry is dropped when it's read
// or evicted, with no timer. Entries carry tags, and dropping a tag drops
// every entry carrying it.
//
// # Route cache
//
// A MemoryProvider stores the responses of pages that call ctx.Cache().Set
// with a MaxAge. Pass it to app.New in cmd/server/main.go:
//
//	site := app.New(lyr.WithCache(cache.NewMemory(cache.MemoryOptions{
//		MaxEntries: 5000,
//		MaxBytes:   256 << 20,
//	})))
//
// Lyr builds the keys, decides whether an entry is fresh or stale and
// serves it. To purge responses on every instance of an app, set
// MemoryOptions.OnInvalidate to publish each purge, and apply the ones
// received with MemoryProvider.InvalidateLocal.
//
// # Data and fragment cache
//
// Store caches any value under a string key. Load runs a loader once per
// key, however many requests miss at the same time. To cache a rendered
// fragment, store its HTML and re-apply its tags to the page's cache
// options, so a purge of a tag drops both:
//
//	var fragments = cache.NewStore(cache.StoreOptions[lyr.HTML]{MaxBytes: 32 << 20})
//
//	html, tags, err := fragments.Load(ctx.Request.Context(), "sidebar", time.Minute, []string{"posts"},
//		func(context.Context) (lyr.HTML, error) {
//			return lyr.RenderHTML(ctx, sidebar)
//		})
//	if err != nil {
//		return err
//	}
//	ctx.Cache().Set(lyr.CacheOptions{Tags: tags})
//
// Values are stored as given, not copied: don't change a value after
// storing it, or one returned by a Store.
package cache
