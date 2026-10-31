# Template syntax

This page is the complete reference for the template part of a `.lyr` file: the grammar, every attribute form and directive, and exactly how each Go value renders.

## Example

```lyr file=src/components/PostMeta.lyr
---
import "time"

type Props struct {
	Title string
	URL   string
	Date  time.Time
	Tags  []string
	Draft bool
	Attrs lyr.Attrs
}
---
{/* The title line of a post. */}
<header class="meta" {...props.Attrs}>
  <h2><a href={props.URL}>{props.Title}</a></h2>
  {if props.Draft}
    <span class="badge">Draft</span>{" "}
  {/if}
  <time datetime={props.Date.Format(time.RFC3339)}>{props.Date.Format("2 Jan 2006")}</time>
  <ul class="tags" hidden={len(props.Tags) == 0}>
    {for _, tag := range props.Tags}
      <li>{tag}</li>
    {/for}
  </ul>
  <script type="application/json" set:json={props.Tags}></script>
  <slot />
</header>
```

```lyr file=src/pages/index.lyr
---
import (
	PostMeta "../components/PostMeta.lyr"
	"time"
)

date := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
tags := []string{"go", "web"}
---
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>Syntax tour</title>
  </head>
  <body>
    <PostMeta title="Hello, Lyr" url="/blog/hello" date={date} {tags} draft attrs={lyr.Attrs{"id": "hello"}}>
      <p>Comments are open.</p>
    </PostMeta>
  </body>
</html>
```

## Grammar

| Construct | Meaning |
|---|---|
| text | Anything except `{` and a `<` that starts a tag; emitted verbatim. `<` starts a tag only when followed by a letter, `/`, `!` or `>` (the fragment shorthand `<>`); any other `<` (`a < b`, `<3`, `<=`) is text. A literal brace is written `{"{"}`. |
| `{expr}` | Any Go expression, escaped (see [How values render](#how-values-render)). Its end is found with a Go-aware scanner that tracks `()[]{}` depth, strings, runes, [template literals](#template-literals) and comments. |
| `{/* … */}` | Template comment; emits nothing. |
| `{if c}` `{else if c}` `{else}` `{/if}` | A Go `if` chain. |
| `{for clause}` `{/for}` | A Go `for` loop with any clause: `range`, three-clause, or condition only. |
| `{switch x}` `{case a, b}` `{default}` `{/switch}` | A Go `switch` statement: with a tag, an init statement, no tag, or a type switch. |
| `<tag …>`, lowercase or containing `-` | An HTML element or custom element (`<my-widget>`). |
| `<Name …>`, capitalized | A component. `Name` must be the alias of a `.lyr` import; otherwise it's an error. |
| `<Self …>` | The current component, rendered recursively. Same props and slots rules as any component. |
| `<Name server:defer …>` | Deferred server rendering: the page gets a placeholder holding the `fallback` slot, and the browser fetches the component's HTML in a separate request. See [Lazy loading](../lazy-loading.md). |
| `<Name server:concurrent …>` | Concurrent server rendering: the component renders in its own goroutine while the page goes on, and its HTML lands in its place. See [`server:concurrent`](#serverconcurrent). |
| `<Name client:idle …>`, also `client:load`, `client:visible`, `client:media` | Controls when the `<script>` modules of the component and its subtree load. See [Lazy loading](../lazy-loading.md). |
| `<slot />`, `<slot name="x">fallback</slot>` | A slot outlet, with optional fallback children. |
| `<Fragment>`, `<>…</>` | Groups children without a wrapper element; mostly used as `<Fragment slot="x">`. `<>…</>` is the shorthand, as in Astro. |
| `<!DOCTYPE …>`, `<!-- … -->` | Passed through to the output. |

## Text

Text is written as it appears in the template, except for whitespace, which is compressed by default (see [Whitespace](#whitespace)). HTML entities such as `&amp;` and `&lt;` pass through unchanged.

A `<` begins a tag only when the next character is a letter, `/` or `!`. Everything else is text, so comparisons and emoticons need no escaping:

```lyr
<p>Use a < b, or <= for "at most". I <3 Go.</p>
```

`{` always starts an expression. To write a literal `{`, use a string expression; a lone `}` is plain text:

```lyr
<code>func main() {"{"} }</code>
```

## Whitespace

Lyr compresses template whitespace when it generates the Go code, following the `compressHTML` setting in `lyr.json` (see [Configuration](../configuration.md#compresshtml)). Whitespace here means spaces, tabs, newlines, carriage returns and form feeds.

**Never altered**, in every mode:

- the content of `<pre>` and `<textarea>` (including nested elements), and of `<script>` and `<style>`;
- HTML comments (`<!-- … -->`) and doctypes;
- attribute values;
- the output of `{expr}`: it's runtime data, so `{" "}` always writes one space.

**`"jsx"`** (the default, as in Astro v7) applies JSX's rule (Babel's) to each text between two tags, expressions or blocks:

- every line is trimmed of spaces and tabs, except at the start of the first line and the end of the last;
- lines that are then empty are dropped;
- the remaining lines are joined with one space.

So whitespace that contains a newline disappears next to a tag or an expression, and whitespace within a line is kept. This template:

```lyr
<ul>
  <li>Posts by <b>{name}</b>
      and friends</li>
</ul>
```

renders `<ul><li>Posts by <b>Ana</b>and friends</li></ul>`: the words join. Write `{" "}` where a space must survive a line break:

```lyr
<li>Posts by <b>{name}</b>{" "}
    and friends</li>
```

**`"html"`** (Astro's `compressHTML: true`; `true` is accepted as a synonym) is lossless for rendering:

- Each run of whitespace becomes one space, or one newline if the run contains a newline.
- A text that is only whitespace is dropped when what can render on each side of it is a block-level boundary: the tag of a block-level element, a doctype, a `<script>` or `<style>`, or the start or end of the template. It's also dropped when the whitespace on one side is kept anyway.
- Anything else counts as inline, and whitespace next to it is kept: text, inline elements (`<a>`, `<b>`, `<span>`, …), `{expr}`, components, `<slot>`, HTML comments, and the start and end of a component's children. So words never join.
- Lyr looks through `{if}`, `{switch}` and `{for}` to find the neighbors: every branch of an `{if}` (and nothing, when it has no `{else}`), every case of a `{switch}` (and nothing, when it has no `{default}`), and zero or more iterations of a `{for}`. Whitespace is dropped only when every possible neighbor allows it.
- Template comments (`{/* … */}`) are removed first, so the whitespace around one is a single run.

The block-level elements are `html`, `head`, `body`, `title`, `meta`, `link`, `base`, `style`, `script`, `noscript`, `template`, `address`, `article`, `aside`, `blockquote`, `details`, `dialog`, `dd`, `div`, `dl`, `dt`, `fieldset`, `figcaption`, `figure`, `footer`, `form`, `h1`–`h6`, `header`, `hgroup`, `hr`, `li`, `main`, `nav`, `ol`, `p`, `pre`, `section`, `summary`, `menu`, `ul`, `br`, `table`, `caption`, `colgroup`, `col`, `thead`, `tbody`, `tfoot`, `tr`, `td`, `th`, `legend`, `optgroup` and `option`.

With `"html"`, the template above renders `<ul><li>Posts by <b>Ana</b>⏎and friends</li></ul>` (⏎ is one newline).

**`false`** writes template text exactly as it appears.

## Expressions

`{expr}` takes any Go expression that produces one value: a variable, a field, a call, an operator expression, a composite literal or a conversion.

```lyr
<p>{props.Title}</p>
<p>{strings.ToUpper(name)}</p>
<p>{len(list)} posts</p>
<p>{map[string]int{"a": 1}["a"]}</p>
<p>{"}"} and {`{raw}`}</p>
```

Braces inside composite literals, strings, runes, template literals and comments don't end the expression. A backtick string is a [template literal](#template-literals) in the template, not a Go raw string.

- Statements aren't expressions: `{x := 1}` is an error. Declare variables in the frontmatter.
- A call that returns several values (`posts.All()` returns a slice and an error) can't be used directly. Call it in the frontmatter and handle the error there.
- Expressions see everything the render body declares, plus `props`, `ctx`, `slots`, `locals`, `params` (in pages) and the `lyr` package. See [Components](../components.md#whats-available-in-the-render-body).

## Template literals

In the template, a backtick string is a JavaScript template literal, as in Astro: static text with Go expressions in `${…}`.

```lyr
<a href={`/fiction/${book.Slug}`} aria-label={`Chapter ${n + 1} of ${book.Title}`}>…</a>
<Card title={`Chapter ${n}`} />
<span>{` · ${book.Words} words`}</span>
<button aria-label={lyr.If(open, `Close ${book.Title}`)}>…</button>
```

renders `<a href="/fiction/the-hollow" aria-label="Chapter 3 of The Hollow &amp; Co">…</a>`, and so on.

- **Everywhere in the template**: attribute values (elements, dynamic tags, components, pass-through attributes), `{…}` content, `set:text`, `set:html`, `set:json`, `class:list` items and keys, spreads, control headers (`` {if s == `a-${b}`} ``, `` {case `x`} ``), and anywhere inside a larger expression (call arguments, `lyr.If`/`lyr.Cond` arguments, operands, inside the `${…}` of another literal). The **frontmatter** and `.go` files are plain Go: a backtick there is a Go raw string, as always. `<script>` and `<style>` content isn't template code.
- **Pieces** are Go expressions of any type, and each prints exactly as `{…}` prints it (see [How values render](#how-values-render)): `` `${n}` `` with an `int` prints `3`, with a `time.Duration` its `String()`, `${false}` prints `false`. Where Lyr differs from JavaScript: an untyped `nil` or a nil interface prints nothing (JavaScript prints `null`/`undefined`), and a slice concatenates its elements (JavaScript joins them with `,`). A `lyr.Renderer` (`${slots.Get("x")}`) is an error: it renders HTML, and a literal is text.
- **A literal is a string.** A `lyr.HTML` piece is escaped like any string, and a `lyr.URL` piece doesn't exempt the value from URL filtering.
- **Escapes** are JavaScript's: `` \` ``, `\$` (so `\${` is a literal `${`), `\\`, `\n`, `\r`, `\t`, `\b`, `\f`, `\v`, `\0`, `\xHH`, `\uHHHH`, `\u{H…}`, `\'`, `\"`, and a backslash before a line break (both dropped). Any other escape is an error, `unknown escape \d in template literal; write \\d for a backslash, or move a Go raw string to the frontmatter`. A `$` not followed by `{` is text.
- **Literals may span lines**; the text keeps its line breaks (`compressHTML` never changes an expression's output). They nest: `` `a ${f(`x ${y}`)}` ``.

**Go raw strings in the template.** A former raw string without `${` or `\` means the same as before (`` {`{raw}`} ``, `` strings.Join(x, `, `) ``). One with a backslash, typically a regular expression (`` regexp.MustCompile(`\d+`) ``), gets the unknown-escape error: double the backslash, or move it to the frontmatter (`re := regexp.MustCompile(`\d+`)`), where it's a Go raw string.

### Where a literal is lowered, and what it costs

- **The whole value of an element attribute or of `{…}`** (only whitespace between the literal and the braces): each piece is written straight to the buffer with its typed write, the static text pre-escaped at build time. No allocation. An attribute is always written as `name="…"`, even when every piece is empty (`title=""`, as Astro writes for an empty string); the boolean-attribute rule doesn't apply, since the value is a string. A `class` attribute alone is written exactly as written; merged with `class:list` (or a pass-through `class`), the literal is one class item, built in a stack buffer (no allocation either). Piece boundaries aren't class boundaries: `` `btn-${size}` `` is one class.
- **A component prop or a pass-through attribute**: one string concatenation, one allocation when a piece is dynamic, the same as Go's `+`, and none for a literal without pieces. The prop's field must be a `string`, a named string type (converted: `` slug={`${a}-${b}`} `` into a `type Slug string`), or an interface the string satisfies (`any`). Anything else is an error: `a template literal is a string; prop count of <Card> is int`.
- **Anywhere else** (a call argument, `lyr.If`'s value, an operand, a control header): a value of type `string`, one concatenation (one allocation when a piece is dynamic). A literal without pieces is an untyped string constant, exactly like `"…"` (`` {case `draft`} ``, `` lyr.CSS(`display:none`) ``). Converting a dynamic one is ordinary Go: `` lyr.CSS(`width:${w}`) `` is an explicit conversion, you vouching for the value. `lyr.If`/`lyr.Cond` evaluate their arguments first, so a literal there is built even when the condition is false.

Constant pieces (`${"x"}`, `${3}`, a `const`) are folded into the static text at build time.

### URLs

In a URL attribute (`href`, `src`, …), the `javascript:`/`vbscript:` filter applies to the **whole value**, as for a string. When the text before the first piece settles the scheme (`/…`, `#…`, `?…`, `./…`, `https://…`, `mailto:…`), nothing is checked at render time: `` href={`/fiction/${slug}`} `` costs only its writes. A literal starting with `javascript:` is replaced at build time (its pieces are still evaluated). Otherwise (`` `${base}/x` ``) the written value is checked once at render time (a few nanoseconds, no allocation), so a scheme split across pieces is caught too.

### Trusted targets and the `style`/`on*` warning

A literal can become a `lyr.HTML`, `lyr.URL` or `lyr.CSS` prop (or a `set:html` value), and doesn't get the `style`/`on*` warning, only when every piece is **inert** by its static type:

- a number or a `bool` (named ones too, `type Pct int`), unless its type has a `String`, `Error` or `Format` method (on `T` or `*T`): a method can return any text;
- a constant (folded, so checked as static text), unless its type has one of those methods;
- a value of the target type itself (a `lyr.URL` piece into a `lyr.URL` prop, a `lyr.CSS` piece on `style`).

So `` <Link href={`/books/${id}`} /> `` works into a `lyr.URL` prop for an `int` id, and `` style={`width:${pct}%`} `` doesn't warn. A `string` piece is never inert: `a string piece can't be part of a lyr.URL prop: make the prop a string (filtered where it's written) or convert the piece with lyr.URL(…)`. The decision is made from the static type, never from the value.

## Comments

| Syntax | Output |
|---|---|
| `{/* note */}` | Nothing. Use it for notes about the template. |
| `<!-- note -->` | Sent to the browser as an HTML comment, verbatim. `{…}` inside it isn't evaluated. |

## Control flow

### `{if}`

```lyr
{if len(list) == 0}
  <p>No posts</p>
{else if len(list) == 1}
  <p>One post</p>
{else}
  <p>{len(list)} posts</p>
{/if}
```

The header is copied into a Go `if` statement, so the condition must be a `bool` expression, and Go's error for a non-boolean condition is reported at the `.lyr` position. An init statement works too, as in Go, in `{if}` and `{else if}`: `{if n, ok := counts[tag]; ok}`. For the other places a template variable can live, see [Variables in the template](../components.md#variables-in-the-template).

### `{for}`

Any Go `for` clause works:

```lyr
{for i, p := range list}<li data-index={i}>{p.Title}</li>{/for}
{for range 3}<span>★</span>{/for}
{for i := 0; i < len(list); i += 2}<hr>{/for}
{for tag := range maps.Keys(counts)}<a href={"/tags/" + tag}>{tag}</a>{/for}
```

The last line ranges over an iterator (`maps.Keys` returns an `iter.Seq`). Each iteration has its own loop variables (Go 1.22 semantics), so closures that capture them are safe.

There's no `{else}` for an empty loop; wrap the loop in `{if len(list) > 0}` instead.

### `{switch}`

`{switch}` picks one of several cases. It compiles to a Go `switch` statement, each case's template as its body:

```lyr file=src/components/StatusBadge.lyr
---
type Props struct {
	Status string
}
---
{switch props.Status}
	{case "draft"}
		<span class="badge">Draft</span>
	{case "scheduled", "queued"}
		<span class="badge">Scheduled</span>
	{default}
		<span class="badge">Published</span>
{/switch}
```

The header is any Go switch header, copied as written, and so is each `{case}` list:

| Header | Example |
|---|---|
| A tag | `{switch post.Status}` with `{case "draft"}`, `{case "scheduled", "queued"}` |
| An init statement and a tag | `{switch s := strings.ToLower(post.Status); s}` |
| No tag: each case is a condition | `{switch}` with `{case n > 10}`, `{case n > 0}` |
| A type switch | `{switch v := item.(type)}` with `{case int}`, `{case posts.Video}`, `{case nil}` |

Go's rules apply: the first matching case wins, there's no fall-through (list several values in one `{case a, b}` instead), and `{default}` runs when no case matches, wherever it's written. Without a `{default}`, a switch with no matching case renders nothing. In a type switch, the variable has the case's type when the case lists one type, so `{v}` in `{case int}` is written as an `int`; in a case that lists several types, and in `{default}`, it keeps the type of the switched value. Go's errors, such as a duplicate constant case or a case of the wrong type, are reported at the `.lyr` position.

Only whitespace and template comments may come between `{switch …}` and its first case, since nothing there could render:

```lyr file=src/components/StatusBadge.lyr expect-error
---
type Props struct {
	Status string
}
---
{switch props.Status}
	<h3>Status</h3>
	{case "draft"}
		Draft
	{default}
		Published
{/switch}
```

```text
src/components/StatusBadge.lyr:7:2: content before the first {case} of a {switch} is never rendered; move it into a {case} or {default}
```

These are errors too:

- `{case}` or `{default}` outside a `{switch}`: `unexpected {case}: no {switch} is open`.
- A second `{default}`: `multiple {default} cases in {switch} (first at 9:2)`.
- `{fallthrough}`: `fallthrough isn't supported in a {switch}; list the values in one {case a, b}`.
- A `{case}` without a list: `{case} needs a value or a condition, e.g. {case "draft"}; use {default} for the rest`.
- `{/case}` or `{/default}`: a case ends at the next `{case}`, `{default}` or `{/switch}`, so there's no closing tag.
- A missing `{/switch}`: `{switch} isn't closed; add {/switch}`.

### Nesting rules

- A block must be closed inside the element where it was opened: `<ul>{for …}<li>…</li>{/for}</ul>` works, while `{if a}<div>{/if}</div>` is a parse error. In the same way, an element opened in a `{case}` must be closed before the next `{case}`.
- Blocks can't appear inside a start tag. To set an attribute conditionally, use its value: `false` and `nil` omit an attribute (see [Attribute values](#attribute-values-on-html-elements)). To add several attributes at once, use a [spread](#spread-attributes).

## Elements

### HTML and custom elements

A tag that is lowercase or contains `-` is an HTML element or a custom element, written to the output as is (with its attributes rendered as described below).

- **Every element must be closed** explicitly. Lyr doesn't auto-close elements the way browsers do, so `<p>`, `<li>`, `<td>` and `<option>` all need their closing tag. A mismatched or missing closing tag is a parse error at its line and column:

  ```lyr file=src/pages/index.lyr expect-error
  <ul>
    <li>Unclosed
  </ul>
  ```

  ```text
  src/pages/index.lyr:3:1: unexpected </ul>: <li> opened at 2:3 isn't closed
  ```

- **Self-closing**: any element may be written self-closed, as in Astro. `<div />` renders `<div></div>`.

- **Void elements** take no closing tag: `area`, `base`, `br`, `col`, `embed`, `hr`, `img`, `input`, `link`, `meta`, `source`, `track`, `wbr`. Both `<br>` and `<br />` are accepted.

- **Raw-text elements**: the content of `<script>` and `<style>` is opaque. Lyr doesn't look for `{…}` there, so you can write JavaScript objects and CSS rules freely. `<title>` and `<textarea>` allow `{expr}`, but other markup inside them is text, as in HTML: `<title>a <b>bold</b> title</title>` renders the `<b>` literally.

- **Scripts and styles**: non-inline `<script>` and `<style>` tags anywhere in a template are extracted and bundled, and scoped styles add a `data-l-…` attribute to every element of the component. A `<script>` with any attribute other than a local `src`, such as `type="application/json"`, is left as it is. See [Styles and scripts](../styles-and-scripts.md).

### Components

A capitalized tag renders a component. Its name must be the alias of a `.lyr` import in the frontmatter (or a variable holding a tag name, a [dynamic tag](#dynamic-tags)):

```lyr
---
import Card "../components/Card.lyr"
---
<Card post={p} />
<Card post={p}><em>with children</em></Card>
```

A tag that matches no import alias and no variable is an error:

```lyr file=src/pages/index.lyr expect-error
---
import Card "../components/Card.lyr"
---
<Card post="hello" />
<Crad post="world" />
```

```text
src/pages/index.lyr:5:2: unknown component <Crad>: no .lyr import with this alias (or declare a variable Crad for a dynamic tag)
```

Only `.lyr` files can be imported as components. A `.tsx`, `.jsx`, `.svelte` or `.vue` import is an error (`framework components aren't supported; use a .lyr component with a <script>`), and so is any other relative or root-relative path that doesn't end in `.lyr`. See [Components](../components.md#no-framework-components).

An import path is relative (`./`, `../`), root-relative (`Card "/src/components/Card.lyr"`, from the project root, as in Astro and Vite) or a `tsconfig.json` `compilerOptions.paths` alias (`Card "@/components/Card.lyr"`), resolved as TypeScript and Astro do. A path that ends in `.lyr` or `.md` and doesn't start with `./`, `../` or `/` must match an alias. See [Root-relative imports](../components.md#root-relative-imports) and [Import aliases](../components.md#import-aliases).

Children of a component are passed as slots. A component with no children allocates nothing for slots, and neither do children in a `lyr build` binary, which calls components directly. In `lyr dev` and `go test`, children that use a variable of the caller allocate one small closure per slot ([Direct calls in production builds](../performance.md#direct-calls-in-production-builds)). Children that are only whitespace pass no slot.

### Dynamic tags

A capitalized tag that names a **variable or constant in scope** renders the HTML element whose name the variable holds, as in Astro (`const Tag = href ? "a" : "button"`):

```lyr file=src/components/Button.lyr
---
type Props struct {
	Type  lyr.Opt[string]
	Attrs lyr.RestAttrs
}

link := props.Attrs.Has("href")
Tag := lyr.Cond(link, "a", "button")
---
<Tag {...props.Attrs} type={lyr.If(!link, props.Type.Or("button"))} class="btn">
  <slot />
</Tag>
```

`<Button href="/new">New</Button>` renders `<a href="/new" class="btn">New</a>`, and `<Button>Save</Button>` renders `<button type="button" class="btn">Save</button>`: one copy of the attributes and children instead of an `{if}` with two.

- **In scope** means declared in the frontmatter (`Tag := …`, `var Tag = …`, or a `const`) or by an enclosing `{for}`, `{if}` or `{switch}` header (`{for _, T := range tags}<T>…</T>{/for}`). A capitalized name is a component when it's an import alias, `Self` or `Fragment`, and a dynamic tag otherwise. A name that is both an import alias and a variable is an error at the variable (`Card is both a component import and a variable; rename one`). Dotted names (`<props.As>`) aren't tags: write `Tag := props.As`.
- **The value is a `string`** (an untyped constant, `lyr.Cond(…)`, `"h" + strconv.Itoa(level)`). Anything else is Go's error at the tag name: `cannot use Tag (variable of type int) as string value in argument to lyr__w.TagOpen`. A named string type needs a conversion: `Tag := string(kind)`.
- **It's evaluated once**, at the start tag, before the attributes. The name is written as given, without changing its case.
- **The name is checked when the page renders**. A name must be an ASCII letter followed by ASCII letters, digits, `-`, `_`, `.` or `:` (HTML, SVG and custom element names). These are render errors at the tag's position, and nothing of the tag is written:
  - an empty name: `dynamic tag Tag is empty; use lyr.Fragment for no element`;
  - any other invalid name: `dynamic tag Tag has an invalid element name "a b"`;
  - `script`, `style`, `textarea`, `title`, `head`, `xmp`, `iframe`, `noembed`, `noframes` and `plaintext`, in any case: `<script> can't be a dynamic tag`. Lyr bundles scripts and styles at build time and writes the page's assets at a static `</head>`, and the others' content isn't parsed as markup; write these elements as static tags;
  - a void element (`img`, `br`, `input`, …) with children or `set:html`/`set:text`: `dynamic tag <img> is a void element and can't have children`. Without content, it's written with no end tag (`<img src="…">`).
- **`lyr.Fragment`** renders no element: with `Wrap := lyr.Cond(wrapped, "div", lyr.Fragment)`, `<Wrap class="w">…</Wrap>` writes only its children when `wrapped` is false, an optional wrapper. Its attributes are then neither written nor **evaluated** (Astro evaluates them and drops them; it matters only for an expression with side effects).
- **Attributes** work as on any HTML element: static values, expressions with typed writes, shorthand, spreads (`lyr.Attrs`, `lyr.AttrList`, pass-through attributes with their `class` merged), `class:list`, `lyr.If`, `lyr.JSON`, the URL filter and the true/false attributes. `set:html`, `set:text` and `slot="x"` work too. `server:*` and `client:*` are errors (`server:defer only works on .lyr components`), and so are `is:inline`, `is:global` and `set:json`.
- **Scoped styles**: the component's scope attribute is written on every dynamic tag, so `h2 { … }` in its `<style>` matches `<Heading>` when it renders an `h2`.
- **Whitespace**: the children are compressed like inline content (the safe choice: the name isn't known at build time). A dynamic `pre` doesn't keep its whitespace verbatim; use a static `<pre>`.
- **Self-closed**: `<Tag />` renders `<div></div>` for a `div`, as for a static tag.

A dynamic tag costs about 10 to 20 ns more than static markup (the name check), and no allocation, a concatenated name included ([Performance](../performance.md#dynamic-tags)). There are no dynamic components: a variable can't hold a component, since each one has its own `Props` type. Use `{switch}` to pick one.

### `<Self>`

`<Self …>` renders the current component again, with new props. It follows the same attribute and slot rules as any other component tag, and takes the same directives. See [Recursion with `<Self>`](../components.md#recursion-with-self).

### `<slot>`

| Outlet | Renders |
|---|---|
| `<slot />` | The default slot: the children that have no `slot` attribute. |
| `<slot name="footer" />` | The children marked `slot="footer"`, in order. |
| `<slot>fallback</slot>`, `<slot name="footer">fallback</slot>` | The slot, or the fallback children if the parent passed nothing for it. |

In the frontmatter and in expressions, `slots.Has("footer")` reports whether a slot was passed (`"default"` names the default slot).

### `<Fragment>`

`<Fragment>` groups children without producing an element. Its main use is sending several elements to one named slot:

```lyr
<Panel title="Contact">
  <Fragment slot="actions">
    <a href="/rss.xml">RSS</a>{" "}
    <a href="/about">About</a>
  </Fragment>
</Panel>
```

As in Astro, `<Fragment>` takes only `slot`, `set:html` and `set:text`. `<Fragment set:html={body} />` writes raw HTML with no wrapper element. Any other attribute on a `<Fragment>` is a compile error.

`<>…</>` is the shorthand, as in Astro: it compiles to exactly the same code as `<Fragment>…</Fragment>`, and the two nest and mix freely. The shorthand takes no attributes, so a fragment sent to a named slot is written `<Fragment slot="x">`:

```lyr
<dl>
  {for _, item := range items}
    <><dt>{item.Term}</dt><dd>{item.Definition}</dd></>
  {/for}
</dl>
```

A `<>` must be closed by `</>`, and a `</>` closes only a `<>`: `<>…</div>` and `<div>…</>` are errors, like any other mismatched end tag.

### Doctype and HTML comments

`<!DOCTYPE html>` and `<!-- … -->` are copied to the output. Lyr inserts the page's CSS and JS links at the first `</head>`, or right after a leading doctype when there is no `</head>`.

## Attributes

| Form | On an HTML element | On a component |
|---|---|---|
| `a` | A bare attribute: `a` | `A: true` |
| `a="s"`, `a='s'` | A static value | `A: "s"` (a Go string literal) |
| `a=bare` | A static unquoted value, as in HTML | `A: "bare"` (a Go string literal, like a quoted value) |
| `a={expr}` | Rendered from the value (see below) | `A: expr` |
| `{a}` | Shorthand for `a={a}` | Shorthand for `a={a}` |
| `{...attrs}` | Spread of a `lyr.Attrs` or a `lyr.AttrList`, or of the component's `lyr.RestAttrs` field | A struct (or pointer to one): its fields [set props](#props-spreads). A `lyr.Attrs`, `lyr.AttrList` or `lyr.RestAttrs`: merged into the component's [pass-through attributes](#pass-through-attributes) (it needs a `lyr.RestAttrs` field) |

Quoted values are literal text: `class="card {extra}"` renders `card {extra}`. To build a value, use a [template literal](#template-literals), `` class={`card ${extra}`} ``, or any expression: `class={"card " + extra}`.

An expression value may contain `>`, quotes and braces; the scanner finds the closing `}` the same way as for `{expr}` (`featured={i > 0}` works).

### Attribute values on HTML elements

| Value | Output |
|---|---|
| `true` | The bare attribute: `<input disabled>` |
| `false`, `nil` | Nothing: the attribute is omitted. |
| a `lyr.Opt[T]` ([optional values](#optional-values-lyropt)) | None: nothing, the attribute is omitted. Some `v`: written as `v` is, by the rules of this table for `T` (so `lyr.Opt[bool]` set to `true` is bare, set to `false` is omitted, or `"false"` on a true/false attribute). |
| `true`/`false` on a true/false attribute | `="true"` or `="false"`: `<button aria-pressed="false">`. These are the attributes whose values are the keywords `true` and `false`: every `aria-*` attribute (any case), and `contenteditable`, `draggable`, `spellcheck` and `writingsuggestions`. |
| a string | The escaped value in quotes. |
| a [template literal](#template-literals), `` `/p/${id}` `` | Always `name="…"` (an empty one is `name=""`), each piece written as `{…}` writes it, escaped; a URL attribute filters the whole value. |
| `lyr.URL` | The escaped value in quotes, without URL filtering. |
| `lyr.CSS` | The escaped value in quotes, like a string (URL attributes still filter it). On `style`, it doesn't warn. |
| `lyr.HTML` | Escaped like any string; attribute values are never raw. |
| `lyr.JSON(v)` (a `lyr.JSONValue`) | `v` encoded as JSON, always written (`false` and `null` too), single-quoted: `data-post='{"id":42}'`. See [JSON attributes](#json-attributes). |
| any other value | Formatted exactly as in [How values render](#how-values-render), then escaped and quoted. A slice concatenates its elements with no separator, so build lists such as class names with [`class:list`](#classlist) or `strings.Join(classes, " ")`. |

On every other attribute, a `bool` is a presence flag: HTML boolean attributes (`disabled`, `hidden`, `open`, …), `data-*` attributes (`data-featured={props.Featured}` renders a bare `data-featured`, or nothing), and every attribute of a custom element (`<my-el open={x}>`). The choice depends only on the attribute's name, so `aria-pressed={x}` on a custom element writes `"true"`/`"false"` too. In a component's `Props`, a `bool` is just a field; the rule applies where it ends up on an element, pass-through attributes included.

#### Conditional values: `lyr.If` and `lyr.Cond`

Go has no ternary. Small runtime helpers stand in for Astro's `cond ? v : undefined`, `x || undefined` and `c ? a : b`:

```lyr
<a href={item.Href} aria-current={lyr.If(item.Section == props.Current, "page")}>{item.Label}</a>
<button title={lyr.Cond(props.Saved, "Remove from library", "Add to library")}>
  {lyr.Cond(props.Saved, "Saved", "Save")}
</button>
```

- `lyr.If(cond, v)` is an [optional value](#optional-values-lyropt), a `lyr.Opt[T]`: set to `v` when `cond` is true, and none otherwise. As an attribute value, none omits the attribute; in content, it writes nothing. `lyr.If(c, nil)` doesn't compile: write nothing instead.
- `lyr.NonZero(x)` is set to `x` unless `x` is its type's zero value (`""`, `0`, `false`, `nil`): Astro's `x || undefined`. `rel={lyr.NonZero(post.Rel)}` is `lyr.If(post.Rel != "", post.Rel)`, with `post.Rel` written once.
- `lyr.Cond(cond, a, b)` is `a` or `b`, with their type, so it's written like any value of that type.
- Which one: `lyr.If` when the false case means **no value** (omit the attribute, write nothing, leave an `Opt` prop unset); `lyr.Cond` when both cases are values.
- For text, `{if}…{else}…{/if}` works too, and writes static text; `lyr.Cond` is shorter for a word.
- Astro's `x ?? "fallback"` is the standard library's `cmp.Or(x, "fallback")`: the first argument that isn't the zero value (`""`, `0`, `nil`).
- **Both arguments are always evaluated**, as for any Go call: `lyr.If(viewer != nil, viewer.Email)` panics when `viewer` is nil, unlike Astro's `viewer ? viewer.email : undefined`. Guard it in the frontmatter (`email := ""` then `if viewer != nil { email = viewer.Email }`), or use `{if viewer != nil}…{/if}`.
- They're plain Go functions, so they work in the frontmatter, in `lyr.Attrs` values and in Go code. When `lyr.If(…)` is the whole value of an HTML element attribute or of a `{…}` in content, `lyr build` and `lyr dev` compile it to an `if` with no boxing and no allocation (see [Performance](../performance.md#conditional-values)); elsewhere it's an ordinary call. Either way the output is the same. As a component prop, pass it to a prop of type `lyr.Opt[T]` ([Optional props](../components.md#optional-props)); for a `string` prop use `lyr.Cond` or a variable.

#### Optional values: `lyr.Opt`

`lyr.Opt[T]` is a value of type `T` that may be absent: Astro's `undefined`. Its zero value is none. Build one with `lyr.Some(v)`, `lyr.None[T]()`, `lyr.If(cond, v)` or `lyr.NonZero(v)`, and read it with `Get()` (the value and whether it's set), `Or(def)` (Astro's `x ?? def`) or `IsSome()`:

```lyr
---
var describedBy lyr.Opt[string] // none
if hint != "" {
	describedBy = lyr.Some(hint)
}
---
<input id="email" aria-describedby={describedBy} />
<p id={lyr.NonZero(post.Slug)}>{lyr.If(post.Draft, " · draft")}</p>
```

- **Written as its value**: on an HTML element attribute, none omits the attribute and a set value is written by the rules for `T` (the URL filter, `lyr.URL` and `lyr.CSS`, `lyr.JSON`, booleans). In content (`{…}`, `set:text`), none writes nothing. A frontmatter variable keeps its type, unlike an `any`, so the value gets the typed writes and no allocation.
- **`class`**: a `lyr.Opt[string]` or `lyr.Opt[[]string]` works as a `class:list` item, and a `lyr.Opt[string]` as a `class={…}` value; none adds no class.
- **`style`**: a `lyr.Opt[lyr.CSS]` is trusted like a `lyr.CSS`, with no warning.
- **`set:html`** takes a `lyr.Opt[lyr.HTML]` as well as a `lyr.HTML`: none writes nothing, some writes the HTML raw (`<div set:html={lyr.If(open, body)}></div>`). A `string` is still a compile error (`set:html needs lyr.HTML or lyr.Opt[lyr.HTML], not string: …`).
- **Only `lyr.Opt` itself counts**: a type defined over it (`type MaybeRel lyr.Opt[string]`) is a new struct type, written like any struct. An alias (`type MaybeRel = lyr.Opt[string]`) is fine.
- In Go code and logs, `fmt.Sprint(o)` prints the value, or `""` for none. In JSON, none is `null`; see [`lyr.Opt`](runtime-api.md#optional-values).

#### JSON attributes

`name={lyr.JSON(v)}` writes `v` encoded with `encoding/json` (struct tags apply), in single quotes, with `'` written as `&#39;` and `&`, `<` and `>` as character references, so the browser decodes it back to the exact JSON text:

```lyr
<like-button data-post={lyr.JSON(props.Post)} data-liked={lyr.JSON(props.Liked)}></like-button>
```

renders `<like-button data-post='{"id":42,"slug":"hello"}' data-liked='false'></like-button>`.

- **Any attribute** can take a JSON value, and an element can have several: `hx-vals={lyr.JSON(vals)}` (htmx) and `x-data={lyr.JSON(state)}` (Alpine) work too.
- **On `data-*` attributes**, a direct `lyr.JSON(…)` call is typed for the component's scripts: `import { data } from "./LikeButton.lyr"`, then `data(this, "post")` has the TypeScript type of `props.Post`. The key is the dataset name (`data-author-name` is `authorName`). See [Client modules](client-modules.md).
- **In a spread** (`lyr.Attrs{"data-x": lyr.JSON(v)}`), it's written the same way, untyped. **In content** (`<pre>{lyr.JSON(cfg)}</pre>`), it's written HTML-escaped like a string. **On a component tag**, it's an ordinary Go value passed to a prop of type `lyr.JSONValue`.
- A type `encoding/json` can never encode (a func, a channel) is a compile error at the argument; a value it can't encode (NaN, a cycle) is a render error at the attribute value's position.

**URL attributes** are filtered at runtime. These are `action`, `background`, `cite`, `codebase`, `data`, `formaction`, `href`, `icon`, `longdesc`, `manifest`, `poster`, `profile`, `src`, `usemap`, `xmlns` and `xlink:href` (the list Go's `html/template` uses, plus `xlink:href`). If the value's scheme, after trimming leading whitespace and control characters and ignoring case, is `javascript:` or `vbscript:`, the value is replaced with `about:invalid#lyr`. Values of type `lyr.URL` skip the filter. The filter applies to expression values and to spread attributes; static values you write in the template are left as written.

HTML attribute names are written to the output exactly as you spell them; only component attributes are matched loosely (see [Name matching](#name-matching)).

### Spread attributes

`{...attrs}` writes every entry of a `lyr.Attrs` (`map[string]any`), sorted by name so the output is stable, or of a `lyr.AttrList`, in list order. Each value follows the rules above (`true` is bare, `false`/`nil` omitted, URL attributes filtered):

```lyr
<input {...props.Attrs} />
```

Each name must match `[A-Za-z_:][-A-Za-z0-9_:.]*`. A name with a space, quote, `>`, `/` or `=` is a render error at the spread's position, and nothing of the spread is written, so a map built from user input can't inject markup.

**Order**: Astro writes a spread object's entries in its key order; a `lyr.Attrs` is a Go map, which has none, so its entries are **sorted by name** (`{...lyr.Attrs{"role": "tab", "id": "t1"}}` writes `id="t1" role="tab"`). Attribute order doesn't change what the browser does, but a snapshot test or a CSS selector on the exact markup can differ after a port. When the order matters, use a **`lyr.AttrList`**, an ordered list built with `lyr.A(name, value)`:

```go
// Attributes are <html>'s theme attributes, in Astro's order.
func Attributes(c Choices) lyr.AttrList {
	return lyr.AttrList{
		lyr.A("data-theme", c.Theme),
		lyr.A("data-dark-theme", c.DarkTheme),
		lyr.A("data-day-theme", c.DayTheme),
	}
}
```

`<html lang="en" {...theme.Attributes(choices)}>` then renders `<html lang="en" data-theme="dark" data-dark-theme="dark" data-day-theme="mist">`, in that order.

- `lyr.A` keeps strings, bools, numbers, `lyr.URL`, `lyr.HTML`, `lyr.CSS` and `lyr.JSONValue`, and a `lyr.Opt` of a string, `bool`, `int`, `int64`, `float64`, `lyr.URL`, `lyr.HTML`, `lyr.CSS` or `lyr.JSONValue` (`lyr.If(cur, "page")`), without boxing them, so a list built in the page costs no allocation (a list a helper returns costs one, its backing array). Any other value is kept as an `any`. Values are written as above.
- An `AttrList` is a plain slice: `append`, `range`, `len` and the `slices` package work on it.
- The same name twice in one list (ignoring ASCII case) is a render error: `attribute "role" twice in spread`.
- `lyr.Attrs` stays the shorter literal when the order doesn't matter.

Pass-through attributes (below) keep the caller's order too.

Attributes are written in source order, with a spread's entries at the spread's position. Lyr doesn't merge duplicates: if a spread repeats an attribute written on the same element, both are written, and browsers use the first one.

**Pass-through attributes**: in a component whose `Props` has a `lyr.RestAttrs` field, `{...props.Attrs}` (the field itself, spelled `props.<Field>`) writes the attributes the caller passed ([Pass-through attributes](#pass-through-attributes)): in the caller's order, then the caller's scope attribute. Their `class` is merged into the element's own `class` or `class:list` (`<button class="btn" {...props.Attrs}>` renders `class="btn mt-2"`, wherever the spread is written), or written as a `class` attribute on an element without one. Other duplicates are written twice, as above.

On a component tag, a spread of a `lyr.Attrs`, a `lyr.AttrList` or a `lyr.RestAttrs` merges into the component's pass-through attributes ([below](#pass-through-attributes)), and a spread of a struct sets props ([Props spreads](#props-spreads)); `<slot>` and `<Fragment>` take none.

## Directives

A directive is an attribute with a namespace (`set:`, `is:`, `server:`, `client:`), `class:list` or the `slot` attribute. Lyr acts on it and doesn't write it to the output.

| Directive | Where | Effect |
|---|---|---|
| `set:html={expr}` | HTML element, `<Fragment>`, `<script>`, `<style>` | Sets the content to raw HTML. `expr` must have type `lyr.HTML` or `lyr.Opt[lyr.HTML]` (none writes no content); a string constant written in the template is also accepted. Any other type, such as a `string` variable, is a compile error at the value's `.lyr` position, also shown in the editor: `set:html needs lyr.HTML or lyr.Opt[lyr.HTML], not string: convert a trusted value with lyr.HTML(…), or use set:text`. |
| `set:text={expr}` | HTML element, `<Fragment>`, `<script>`, `<style>` | Sets the content to the escaped text of `expr`. |
| `set:json={expr}` | `<script type="application/json">` only | Sets the content to `expr` encoded as JSON, escaped for a script element. See [`set:json`](#setjson). |
| `class:list={…}` | HTML element, component tag | Builds the `class` attribute (on a component, its `class` prop) from a list of classes and conditions. See [`class:list`](#classlist). |
| `slot="name"` | A child of a component | Sends that child to the named slot instead of the default slot. |
| `is:inline` | `<script>`, `<style>` | Leaves the tag untouched: no extraction, bundling or scoping. |
| `is:global` | `<style>` | Skips CSS scoping. |
| `server:defer` | Component tag | Renders the component in a separate request after the page loads. See [`server:defer`](#serverdefer). |
| `server:concurrent` | Component tag | Renders the component in its own goroutine, overlapping it with the rest of the page. See [`server:concurrent`](#serverconcurrent). |
| `client:load`, `client:idle`, `client:visible`, `client:media` | Component tag | Controls when the scripts of the component and its subtree load. See [`client:*`](#client). |

An element with a `set:` directive takes no children (whitespace aside); the directive replaces its content. `set:` directives on a component are a compile error.

On a `<script>` or `<style>`, `set:html` and `set:text` make the element **inline**, as in Astro: it's written where it is, with that content, and isn't compiled, bundled or scoped, with or without `is:inline`. This is the usual way to emit a script or style built in Go, such as a theme bootstrap script:

```lyr
---
themeInit := lyr.HTML(`document.documentElement.dataset.theme = localStorage.theme ?? "light";`)
---
<script is:inline set:html={themeInit}></script>
```

The content isn't escaped for JavaScript or CSS: `set:html` writes it as is, so it must be trusted (and must not contain `</script>` or `</style>`), and `set:text` HTML-escapes it like any text (`<` becomes `&lt;`, which a script or style doesn't decode), as Astro does. For data, use `set:json` instead.

The compiler **warns** (without failing) on `on*={expr}` attributes such as `onclick={…}` and on `style={expr}`, on HTML elements and when passed through a `lyr.RestAttrs` field: escaping makes a value safe in HTML, not in JavaScript or CSS. A constant value (`style={"display: none"}`, a `const`) doesn't warn, and neither does a `style` value of type `lyr.CSS` (exactly that type; a type defined from it warns). A component whose Go code doesn't type-check warns on every such attribute. See [Event handler and style attributes](../components.md#event-handler-and-style-attributes).

### `class:list`

`class:list` builds the `class` attribute from a list, as in Astro. Its value is a comma-separated list of items, written like the elements of a Go composite literal:

```lyr
---
type Props struct {
	Class  string
	Hidden bool
	Pairs  bool
}

base := []string{"grid", "gap-2"}
---
<input class:list={"appearance-none", "rounded-input border", "hidden": props.Hidden} />
<div class:list={"block", props.Class}>…</div>
<ul class:list={base, "grid-cols-2": props.Pairs, "flex": !props.Pairs}>…</ul>
```

With `Hidden` true, `Class` set to `"mt-4"` and `Pairs` false, this renders `<input class="appearance-none rounded-input border hidden">`, `<div class="block mt-4">` and `<ul class="grid gap-2 flex">`.

| Item | Adds |
|---|---|
| `"a b"` | The classes in the string. |
| An expression of a string type (`props.Class`) | The classes in its value. |
| An expression of type `[]string` (`base`) | The classes of each element, as Astro's nested arrays do. |
| `"c": cond` | The classes of `"c"` when `cond`, a `bool`, is true: Astro's object form `{ c: cond }`. The key is usually a string literal; any string expression works. |

Conditional classes, side by side with Astro:

| Astro | Lyr |
|---|---|
| `class:list={["card", { featured }]}` | `class:list={"card", "featured": props.Featured}` |
| `class:list={[clamped && "group", "outline-none"]}` | `class:list={"group": clamped, "outline-none"}` |
| `class:list={[on ? "a" : "b"]}` | `class:list={lyr.Cond(on, "a", "b")}`; or `"a": on, "b": !on` |
| `class:list={[wrapped ? undefined : className]}` | `class:list={props.Class: !wrapped}` (the key can be any string) |
| `class={on ? "a" : "b"}` | `class={lyr.Cond(on, "a", "b")}` |
| `class={on ? "a" : undefined}` | `class={lyr.If(on, "a")}` (no `class` attribute when false) |
| `class:list={[className ?? "p-4"]}` | `class:list={cmp.Or(props.Class, "p-4")}` |

All of these allocate nothing on an HTML element. `lyr.Cond` and `lyr.If` evaluate both arguments ([Conditional values](#conditional-values-lyrif-and-lyrcond)).

- **Order and whitespace**: classes are written in list order, separated by single spaces, whatever whitespace the items hold. Empty items are dropped; duplicates are kept, as in Astro.
- **An empty list writes no `class` attribute** (`class:list={"active": false}` renders `<div>`).
- **Escaping**: classes are escaped like any attribute value.
- **A `class` attribute on the same element is merged in**, first, wherever it's written: `<p class="card" class:list={"featured": on}>` renders `<p class="card featured">`, at the position of `class:list`. A `lyr.Attrs` spread's `class` isn't merged: both are written, and browsers use the first. [Pass-through attributes](#pass-through-attributes)' class is merged, after the list's classes.
- **On a component tag**, the classes are joined into its `class` prop, so the component's `Props` needs a `Class string` field, or a `lyr.RestAttrs` field, which takes the joined classes as its class ([Pass-through attributes](#pass-through-attributes)). Without either, it's an error at `class:list`: `class:list sets the class prop, which <Card> doesn't have (props: title)`.
- **Types**: an item is a string kind or a `[]string`, or a `lyr.Opt` of one (none adds nothing: `class:list={"card", props.Extra}` with `Extra lyr.Opt[string]`). An item of any other type is a build error at the item, `class:list items must be a string or []string, not int`. A condition that isn't a `bool` is Go's error at the condition.
- **Syntax**: a nested list (`{"a", {"b": on}}`) is an error, `class:list items can't be nested lists; list the classes directly, or use a []string`; so is a value that isn't in braces, `class:list takes a list in braces, e.g. class:list={"card", "active": active}`. A tag takes one `class:list`; `<slot>`, `<Fragment>`, `<script>` and `<style>` take none.
- **Cost**: static classes are joined into the surrounding HTML at build time and each condition becomes an `if`, so a `class:list` renders as fast as hand-written markup and, on an HTML element, allocates nothing. On a component, joining items that aren't all static allocates the prop's string (nothing when a single item is already clean).

### `set:json`

`set:json` passes server data to client scripts. It's only valid on a `<script type="application/json">`, which Lyr never processes or bundles:

```lyr
<script type="application/json" id="tags" set:json={props.Tags}></script>
```

- `expr` can have any type `encoding/json` can encode, and `json` struct tags apply. A type it can never encode (a func, a channel) is a compile error at the value; a value it can't encode (NaN, a cycle) is a render error at the `set:json` attribute's position. A `lyr.JSON(x)` value writes `x`.
- The output is escaped for the script element: `<`, `>`, `&`, U+2028 and U+2029 become `\u003c`, `\u003e`, `\u0026`, `\u2028` and `\u2029`. A value containing `</script>` or `<!--` can't end the element.
- A script reads it with `json(id)` from the component's module, typed from `expr`: `import { json } from "./PostMeta.lyr"`, then `json("tags")` is a `string[] | null`. Only a static `id` is typed; with a dynamic `id={…}` (or none), read it with `JSON.parse(document.getElementById(…).textContent)`. See [Client modules](client-modules.md#jsonid).
- On any other element, `set:json` is a compile error: `set:json only works on <script type="application/json">`.

For per-element data, use [`lyr.JSON`](#json-attributes) or plain `data-*` attributes. All three patterns, with custom-element examples, are in [Components](../components.md#passing-data-to-client-scripts).

### `server:defer`

`server:defer` is a bare attribute on a component tag (`<Self>` included). Instead of rendering the component, the page writes a placeholder with the component's `fallback` slot, and the browser fetches the component's HTML after the page loads:

```lyr
<Comments server:defer postID={post.Slug}>
  <p slot="fallback">Loading comments…</p>
</Comments>
```

- Props are passed as usual, and every field of the component's `Props` must be JSON-serializable; that's checked when Lyr generates code.
- The only allowed children are elements or `<Fragment>`s with `slot="fallback"`, plus whitespace. With no fallback, the placeholder is empty.

| Mistake | Compile error |
|---|---|
| Any other child | `only slot="fallback" content is allowed inside a server:defer component` |
| On an HTML element, `<slot>` or `<Fragment>` | `server:defer only works on .lyr components` |
| With a value (`server:defer="x"`) | `server:defer takes no value` |

### `server:concurrent`

`server:concurrent` is a bare attribute on a component tag (`<Self>` included). The component, with its whole subtree and the slot content passed to it, renders in its own goroutine, while the page keeps rendering after it. When the page is done, Lyr waits for every marked component and assembles the response in source order, so the HTML is byte-identical to rendering in order. Two marked components that each wait 10 ms for their data take about 10 ms together instead of 20:

```lyr
<Weather server:concurrent city="Paris" />
<LatestPosts server:concurrent limit={5} />
```

- The component starts when the render reaches the tag, after its props and slot content are evaluated, as for any call. Only marked components leave the goroutine: an unmarked slow component still blocks what follows it, so mark every slow component.
- Errors come out in source order: the page fails with the error the in-order render would have given. A redirect or `lyr.ErrNotFound` from a marked component works as usual. Lyr doesn't cancel the others; the response waits for every marked component that started.
- `ctx.SetStatus` and `ctx.Header()` in a marked component apply when the page is assembled, in source order, after the page's own.
- It works with `client:*` on the same tag, in `{for}` loops, and on `<Self>`. At most 16 marked components of one render run at once (`lyr.WithConcurrencyLimit`); past that, they render in order, with the same output.
- Outside a request (a `Writer` from `lyr.NewWriter` in your own Go code), it renders in order.

See [Rendering](../rendering.md#slow-components) for when to use it, and [Components](../components.md#fetching-data-in-components) for the data-race rules.

| Mistake | Compile error |
|---|---|
| On an HTML element, `<slot>` or `<Fragment>` | `server:concurrent only works on .lyr components` |
| With a value (`server:concurrent="x"`) | `server:concurrent takes no value` |
| Together with `server:defer` | `server:defer and server:concurrent can't be combined; a deferred component already renders in its own request` |

### `client:*`

A `client:*` directive on a component tag controls when the `<script>` modules of the component, and of every component it renders, load in the browser. The HTML is always rendered on the server.

| Directive | Scripts load |
|---|---|
| `client:load` | With the page. The same as no directive. |
| `client:idle` | Once the browser is idle (`requestIdleCallback`, or a `setTimeout` where it's missing). |
| `client:visible`, `client:visible="200px"` | When an instance scrolls into view. The optional value is the `IntersectionObserver` `rootMargin`. |
| `client:media="(max-width: 600px)"` | When the media query matches, at page load or later. The value is required. |

Values must be static strings. These are compile errors:

| Mistake | Compile error |
|---|---|
| On an HTML element, `<slot>` or `<Fragment>` | `client:visible only works on .lyr components; move the <script> into a component` |
| An expression value (`client:visible={margin}`) | `client:visible takes a static string value, not an expression` |
| A value on `client:load` or `client:idle` | `client:idle takes no value` |
| `client:media` without a query | `client:media needs a media query, e.g. client:media="(max-width: 600px)"` |
| Two `client:*` directives on one tag | `only one client:* directive is allowed per component` |
| `client:only` | `client:only is for framework components, which Lyr doesn't support; use client:load, client:idle, client:visible or client:media` |

The messages name the directive you wrote. If a component's scripts also load eagerly elsewhere on the same page, they stay eager. See [Lazy loading](../lazy-loading.md#client-lazy-scripts) for the details.

### Combining and reserved namespaces

`server:defer` and a `client:*` directive on the same tag is a compile error:

```lyr file=src/pages/blog/[slug].lyr expect-error
---
import Card "../components/Card.lyr"
---
<Card server:defer client:idle />
```

```text
src/pages/blog/[slug].lyr:4:7: server:defer and client:* can't be combined; put client:* on a component inside the deferred one
```

The `server:` and `client:` namespaces are reserved. An unknown directive in either is a compile error at the attribute, listing the valid ones:

```lyr file=src/pages/index.lyr expect-error
---
import Card "../components/Card.lyr"
---
<Card client:hover />
<Card server:lazy />
```

```text
src/pages/index.lyr:4:7: unknown directive "client:hover" (client directives: client:load, client:idle, client:visible, client:media)
src/pages/index.lyr:5:7: unknown directive "server:lazy" (server directives: server:defer, server:concurrent)
```

Astro's `define:vars` isn't supported, and using it is a compile error with a hint, rather than being written out as a plain attribute:

```lyr file=src/components/Card.lyr expect-error
---
type Props struct {
	Featured bool
}
---
<article class:list={"card", "featured": props.Featured}>
  <h2>Hello</h2>
</article>
<script define:vars={props}>
  console.log("card");
</script>
```

```text
src/components/Card.lyr:9:9: define:vars isn't supported; pass data with lyr.JSON on a data-* attribute, or set:json
```

Other attributes with a colon, such as `xlink:href` or `xml:lang`, are ordinary HTML attributes.

## Component attributes and props

On a component tag, each attribute (other than a directive) sets a field of the target component's `Props`. Pages don't have `Props`: declaring `type Props` in a page is a build error (`pages don't receive props; move this to a component or read params`).

### Name matching

The attribute name is matched against the `Props` field names, **ignoring case, `-` and `_`**:

| Attribute | Field |
|---|---|
| `title` | `Title` |
| `id` | `ID` |
| `url` | `URL` |
| `is-open`, `isOpen`, `is_open` | `IsOpen` |
| `user-id`, `userId` | `UserID` |

Two fields of one `Props` that normalize to the same name (`IsOpen` and `Is_Open`) are an error.

When Lyr can't list the target's fields from its source, because `Props` embeds another struct or is an alias (`type Props = other.T`), it converts the attribute to PascalCase, writing each word found in Go's common-initialism table in capitals: `user-id` becomes `UserID`, `api-url` becomes `APIURL`. The table is the one Go linters use: `ACL`, `API`, `ASCII`, `CPU`, `CSS`, `DNS`, `EOF`, `GUID`, `HTML`, `HTTP`, `HTTPS`, `ID`, `IP`, `JSON`, `LHS`, `QPS`, `RAM`, `RHS`, `RPC`, `SLA`, `SMTP`, `SQL`, `SSH`, `TCP`, `TLS`, `TTL`, `UDP`, `UI`, `UID`, `UUID`, `URI`, `URL`, `UTF8`, `VM`, `XML`, `XMPP`, `XSRF`, `XSS`.

A page's route params get their `params` field names from this PascalCase conversion: `[post-id]` becomes `params.PostID`, `[id]` becomes `params.ID` (see [Route params](../routing.md#field-names)).

### Values

- `a="s"` and `a=bare` become the Go string literal `"s"`, so they only fit a `string` field (or a named string type).
- A bare `a` becomes `true`.
- `a={expr}` passes `expr` as is; the Go compiler checks its type against the field.
- `` a={`…${x}…`} ``, a [template literal](#template-literals), is a string: the field must be a `string`, a named string type or an interface such as `any` (a `lyr.HTML`, `lyr.URL` or `lyr.CSS` field only with [inert pieces](#trusted-targets-and-the-styleon-warning)).
- A field you don't set has its zero value.

### Unknown props

An attribute that matches no field is reported by Lyr at the attribute name, in your spelling, with the names that would work (in lower camel case, such as `isOpen`). With the `PostMeta` component of the [example](#example):

```lyr file=src/pages/index.lyr expect-error
---
import PostMeta "../components/PostMeta.lyr"
---
<PostMeta titel="Hello, Lyr" />
```

```text
src/pages/index.lyr:4:11: unknown prop "titel" on <PostMeta> (props: title, url, date, tags, draft, attrs; add a lyr.RestAttrs field to pass HTML attributes through)
```

A component with a `lyr.RestAttrs` field never reports an unknown prop: the attribute is passed through ([below](#pass-through-attributes)).

When Lyr can't list the fields (the embed and alias case above), it skips this check. Go then reports the unknown field, still at the attribute name, with the type name rewritten to your alias. With this `Card`, whose `Props` embeds a struct:

```lyr file=src/components/Card.lyr
---
type Link struct {
	Post string
}

type Props struct {
	Link
	Featured bool
}
---
<article>{props.Post}</article>
```

a misspelled attribute is reported by Go:

```lyr file=src/pages/index.lyr expect-error
---
import Card "../components/Card.lyr"
---
<Card psot="hello" />
```

```text
src/pages/index.lyr:4:7: unknown field Psot in struct literal of type Card.Props
```

### Pass-through attributes

A `Props` field of type `lyr.RestAttrs` collects the call site's attributes that match no other field (see [Components](../components.md#passing-html-attributes-through) for the guide). The type is the marker; the field's name is yours (`Attrs` by convention).

| Written on the tag | Passed as |
|---|---|
| `a="s"`, `a='s'`, `a=bare` | A static attribute `a="s"`, escaped at build time. |
| `a` | A bare attribute. |
| `a={expr}`, `{a}` | A dynamic attribute, written by the [attribute value rules](#attribute-values-on-html-elements): `true` bare, `false`/`nil` omitted, URL attributes filtered, `lyr.URL` trusted, `lyr.JSON(v)` single-quoted. |
| `class`, `class:list` | The joined classes, kept apart and merged into the class of the element the attributes are spread on. With a `Class` prop, they set the prop instead. |
| `{...x}` (`x` a `lyr.RestAttrs` or `lyr.Attrs`) | Merged in: attributes written on the tag win over the spread's for the same name (`class` included); of two spreads, the first wins. |
| `attrs={x}` | Sets the field itself, like any prop (forwarding a value built in Go). |

- **Names** are kept as written, and attributes in source order. Directives (`slot`, `server:defer`, `client:*`, `set:*`, …) aren't passed.
- **The caller's scope attribute** (`data-l-…`, when the calling file has a scoped `<style>`) is passed too, so the caller's scoped rules match the element it lands on. Components without a `lyr.RestAttrs` field never get it.
- **On the element**, `{...props.Attrs}` writes them (see [Spread attributes](#spread-attributes)). In Go, `props.Attrs.Has("href")` and `props.Attrs.Get("href")` read one ([Runtime API](runtime-api.md#restattrs)).
- **Declaration**: at most one `lyr.RestAttrs` field, written `lyr.RestAttrs`, in a `Props` without embedded fields.

Errors, each at its position:

```text
src/components/Button.lyr:6:2: Props has two lyr.RestAttrs fields (Attrs, Extra); a component takes one
src/components/Button.lyr:6:2: a lyr.RestAttrs field needs a Props without embedded fields, so Lyr can tell props from pass-through attributes
src/pages/index.lyr:4:28: attribute name is passed twice to <Button>
src/pages/index.lyr:4:2: <Button> gets attrs={…} and pass-through attributes; use {...x} to merge them
src/pages/index.lyr:4:7: {...} spread on <Card> needs a lyr.RestAttrs field in its Props, to pass HTML attributes through
src/pages/index.lyr:4:15: class={…} merged with pass-through attributes must be a string or []string, not int
```

The same name twice is an error even in another case (`name` and `NAME`). A `class={…}` passed through, or written next to `{...props.Attrs}`, is a class list item, so it must be a `string` or `[]string`.

### Props spreads

On a component tag (`<Self>` included), `{...x}` whose type is a struct or a pointer to a struct sets props (see [Components](../components.md#spreading-props) for the guide). Lyr tells it from a pass-through spread by the type, so these rules are checked by the build's type check (in the editor, after a short pause).

- **Fields**: each exported field of `x`, including fields promoted through embedded structs (`encoding/json`'s field set, tags ignored), sets the callee's `Props` field of the same Go name (exact: `QuestionID`, not `questionId`). Unexported fields are skipped. Every field must be a prop.
- **Types**: Go's assignability, plus `T` into a `lyr.Opt[T]` prop as `lyr.Some(v)`.
- **Order**: attributes and spreads apply in source order and a later one wins; an attribute before a spread that sets the same field, and two spreads that set the same field, are errors. Each operand is evaluated once, in source order; pass-through attributes written on the tag are evaluated after the props.
- **A nil pointer** spreads nothing.
- **The callee's `lyr.RestAttrs` field**, when `x` has it, is merged with the attributes passed through on the tag (which win), not overwritten.
- `server:defer`, `server:concurrent` and `client:*` work as with attributes. A Markdown import takes no props, so a spread on one is the unknown-prop error.

Errors, each at its position:

```text
src/pages/index.lyr:4:7: {...card} on <Card>: cards.Summary has fields that aren't props of Card (Blurb, Rank); spread a struct whose fields are all props, or pass them one by one (props: href, title, lines)
src/pages/index.lyr:5:7: {...card} sets Lines of <Card>: card.Lines is int, the prop is lyr.Opt[string]
src/pages/index.lyr:6:7: questionID="…" is overwritten by {...actions} after it (it sets QuestionID); move it after the spread to override
src/pages/index.lyr:7:16: {...ref} and {...options} both set Slug of <Cached>; a spread can't override another
src/pages/index.lyr:8:7: {...n} on <Card>: n is int; spread a struct (its fields set props) or a lyr.Attrs, lyr.AttrList or lyr.RestAttrs (pass-through attributes)
src/pages/index.lyr:9:9: {...x} can't set Lines of <Cached>: it's promoted through the embedded pointer *cards.Options
src/pages/index.lyr:10:7: {...x} can't read Lines: it's promoted through the embedded pointer *cards.Options; pass it as an attribute
src/pages/index.lyr:11:7: unknown prop {...x} on <Intro> (Intro takes no props)
```

When the build can't run its type check (it warns that it can't read export data), a struct spread fails the render at its position: `{...card} on <Card> sets props, which needs Lyr's type check; it was skipped (see the warning above)`.

## How values render

`{expr}` writes its value according to the value's type. Lyr picks the write at compile time from the static type, and falls back to a runtime check for interface values; both give byte-identical output.

| Type | Renders as | Example → output |
|---|---|---|
| untyped `nil`, or a nil interface value | nothing | `{nil}` → `` |
| `string` | escaped text | `{"a < b"}` → `a &lt; b` |
| `lyr.HTML` | raw, unescaped | `{lyr.HTML("<b>hi</b>")}` → `<b>hi</b>` |
| `lyr.URL` | escaped text | `{lyr.URL("/a?b=1&c=2")}` → `/a?b=1&amp;c=2` |
| `lyr.CSS` | escaped text | `{lyr.CSS(`a:"b"`)}` → `a:&quot;b&quot;` |
| `[]byte` | escaped text, not a list of numbers | `{[]byte("hi")}` → `hi` |
| `int`, `int8`…`int64`, `rune` | decimal | `{42}` → `42`, `{'a'}` → `97` |
| `uint`, `uint8`…`uint64`, `byte` | decimal | `{byte(65)}` → `65` |
| `float64` | as JavaScript's `String(x)`, what Astro renders | `{0.1}` → `0.1`, `{2.0}` → `2`, `{1e6}` → `1000000`, `{1e-7}` → `1e-7`, `{1e21}` → `1e+21` |
| `float32` | the same, with the shortest float32 digits | `{float32(0.1)}` → `0.1` (not `0.10000000149011612`) |
| `bool` | `true` or `false` (unlike Astro, where `{false}` renders nothing) | `{ok}` → `true` |
| `error` | its escaped `Error()` text; a nil error renders nothing | `{err}` → `file not found` |
| `fmt.Stringer` | its escaped `String()` text | `{time.Second}` → `1s` |
| `lyr.Renderer`, `func(*lyr.Writer) error` | called; whatever it writes is output | `{slots.Get("footer")}` |
| `lyr.Opt[T]` | nothing when none; else its value, by these rules for `T` (before the `error` and `fmt.Stringer` rows: an `Opt[lyr.HTML]` is raw) | `{lyr.If(draft, " · draft")}` → ` · draft` or `` |
| a slice of the above | each element in order, no separator | `{[]string{"a", "b"}}` → `ab` |
| anything else | `fmt.Sprint`, escaped | `{struct{ X int }{1}}` → `{1}` |

Details that are easy to trip over:

- **Characters are numbers**: a `rune` or `byte` prints its code (`{'a'}` → `97`). Use `{string(r)}` for the character.
- **Named types** print like their underlying value: `type Slug string` prints the string, `type Count int` the number, `type Price float64` the float as above. If the type has a `String` method, that's used instead.
- **Only `lyr.HTML` itself is raw.** A type defined from it (`type Markup lyr.HTML`) is escaped like a string, and a type defined from `lyr.URL` is URL-filtered like a string. Convert back with `lyr.HTML(m)` to write raw.
- **`String` on a pointer receiver** is only used when the value is a pointer: a `T` value whose `String` is declared on `*T` prints with `fmt.Sprint` (its fields), not with `String`.
- **Pointers aren't dereferenced**: a `*string` prints an address. Write `{*p}`, after checking `p != nil`.
- **Floats** print as JavaScript prints numbers, so a ported Astro page renders the same text: the shortest digits that read back as the same value, in decimal notation from `0.000001` up to (not including) `1e21`, and exponent notation outside it (`1e-7`, `1.5e+21`). `-0` prints `0`; `NaN`, `Infinity` and `-Infinity` print as in JavaScript. This is not `fmt.Sprint`'s form (`1e+06`, `1e-07`), which a float inside another value still gets (`{struct{ F float64 }{1e6}}` → `{1e+06}`). `set:json` and `lyr.JSON` use `encoding/json`, whose numbers already match JavaScript's (`1e-7`, `1e+21`), apart from `NaN` and `±Inf`, which are an error there. For a fixed format, call `strconv.FormatFloat` or `fmt.Sprintf("%.2f", x)`.
- **`time.Time`** is a `fmt.Stringer`, so `{date}` prints `2026-10-01 00:00:00 +0000 UTC`. Use `{date.Format("2 Jan 2006")}` for a readable date.

Attribute values follow the same formatting, with the attribute rules for `true`, `false`, `nil`, a none `lyr.Opt` and URLs on top (see [Attribute values](#attribute-values-on-html-elements)).

## See also

- [Components](../components.md): frontmatter, props, slots, recursion, passing data to scripts and escaping, with examples
- [Lazy loading](../lazy-loading.md): `server:defer` and `client:*` in depth
- [Rendering](../rendering.md#slow-components): slow components and `server:concurrent`
- [Styles and scripts](../styles-and-scripts.md): `<style>`, `<script>`, `is:inline` and `is:global`
- [Runtime API](runtime-api.md): `lyr.HTML`, `lyr.URL`, `lyr.JSON`, `lyr.Attrs`, `lyr.Slots`, `lyr.Renderer`
- [Client modules](client-modules.md): `data` and `json` in scripts, and the Go-to-TypeScript type mapping
- [CLI](cli.md): where errors and warnings are reported
