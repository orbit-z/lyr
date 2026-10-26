package orbit

import "context"

// orbitLoads returns two loads whose func literals line directives map to
// a .orbit file, as generated code does: the first (line 10) waits for its
// context; the second (line 20) panics with p on line 22 when p isn't nil,
// and returns err otherwise.
func orbitLoads(err error, p any) []func(context.Context) error {
	return []func(context.Context) error{
//line src/pages/load.orbit:10
		func(rc context.Context) error { <-rc.Done(); return nil },
//line src/pages/load.orbit:20
		func(rc context.Context) error {
			if p != nil {
				panic(p)
			}
			return err
		},
	}
}
