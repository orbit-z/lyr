# Template syntax

This page is the complete reference for the template part of a `.orbit` file: the grammar, every attribute form and directive, and exactly how each Go value renders.

## Example

```orbit file=src/components/PostMeta.orbit
---
import "time"

type Props struct {
    Title string
    URL   string
    Date  time.Time
    Tags  []string
    Draft bool
    Attrs orbit.Attrs
}
---
{/* The title line of a post. */}
<header class="meta" {...props.Attrs}>
  <h2><a href={props.URL}>{props.Title}</a></h2>
  {if props.Draft}
    <span class="badge">Draft</span>
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

```orbit file=src/pages/index.orbit
---
import (
    "time"
    PostMeta "../components/PostMeta.orbit"
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
    <PostMeta title="Hello, Orbit" url="/blog/hello" date={date} {tags} draft attrs={orbit.Attrs{"id": "hello"}}>
      <p>Comments are open.</p>
    </PostMeta>
  </body>
</html>
```

## Grammar

| Construct | Meaning |
|---|---|
| text | Anything except `{` and a `<` that starts a tag; emitted verbatim. `<` starts a tag only when followed by a letter, `/` or `!`; any other `<` (`a < b`, `<3`, `<=`) is text. A literal brace is written `{"{"}`. |
| `{expr}` | Any Go expression, escaped (see [How values render](#how-values-render)). Its end is found with a Go-aware scanner that tracks `()[]{}` depth, strings, runes, raw strings and comments. |
| `{/* … */}` | Template comment; emits nothing. |
| `{if c}` `{else if c}` `{else}` `{/if}` | A Go `if` chain. |
| `{for clause}` `{/for}` | A Go `for` loop with any clause: `range`, three-clause, or condition only. |
| `<tag …>`, lowercase or containing `-` | An HTML element or custom element (`<my-widget>`). |
| `<Name …>`, capitalized | A component. `Name` must be the alias of an `.orbit` import; otherwise it's an error. |
| `<Self …>` | The current component, rendered recursively. Same props and slots rules as any component. |
| `<Name server:defer …>` | Deferred server rendering: the page gets a placeholder holding the `fallback` slot, and the browser fetches the component's HTML in a separate request. See [Lazy loading](../lazy-loading.md). |
| `<Name client:idle …>`, also `client:load`, `client:visible`, `client:media` | Controls when the `<script>` modules of the component and its subtree load. See [Lazy loading](../lazy-loading.md). |
| `<slot />`, `<slot name="x">fallback</slot>` | A slot outlet, with optional fallback children. |
| `<Fragment>` | Groups children without a wrapper element; mostly used as `<Fragment slot="x">`. |
| `<!DOCTYPE …>`, `<!-- … -->` | Passed through to the output. |

`{switch}` blocks aren't supported yet; use `{if}`/`{else if}`.

## Text

Text is written as it appears in the template, except for whitespace, which is compressed by default (see [Whitespace](#whitespace)). HTML entities such as `&amp;` and `&lt;` pass through unchanged.

A `<` begins a tag only when the next character is a letter, `/` or `!`. Everything else is text, so comparisons and emoticons need no escaping:

```orbit
<p>Use a < b, or <= for "at most". I <3 Go.</p>
```

`{` always starts an expression. To write a literal `{`, use a string expression; a lone `}` is plain text:

```orbit
<code>func main() {"{"} }</code>
```

## Whitespace

Orbit compresses template whitespace when it generates the Go code, following the `compressHTML` setting in `orbit.json` (see [Configuration](../configuration.md#compresshtml)). Whitespace here means spaces, tabs, newlines, carriage returns and form feeds.

**Never altered**, in every mode:

- the content of `<pre>` and `<textarea>` (including nested elements), and of `<script>` and `<style>`;
- HTML comments (`<!-- … -->`) and doctypes;
- attribute values;
- the output of `{expr}`: it's runtime data, so `{" "}` always writes one space.

**`"html"`** (the default, Astro's `compressHTML: true`; `true` is accepted as a synonym) is lossless for rendering:

- Each run of whitespace becomes one space, or one newline if the run contains a newline.
- A text that is only whitespace is dropped when what can render on each side of it is a block-level boundary: the tag of a block-level element, a doctype, a `<script>` or `<style>`, or the start or end of the template. It's also dropped when the whitespace on one side is kept anyway.
- Anything else counts as inline, and whitespace next to it is kept: text, inline elements (`<a>`, `<b>`, `<span>`, …), `{expr}`, components, `<slot>`, HTML comments, and the start and end of a component's children. So words never join.
- Orbit looks through `{if}` and `{for}` to find the neighbors: every branch of an `{if}` (and nothing, when it has no `{else}`), and zero or more iterations of a `{for}`. Whitespace is dropped only when every possible neighbor allows it.
- Template comments (`{/* … */}`) are removed first, so the whitespace around one is a single run.

The block-level elements are `html`, `head`, `body`, `title`, `meta`, `link`, `base`, `style`, `script`, `noscript`, `template`, `address`, `article`, `aside`, `blockquote`, `details`, `dialog`, `dd`, `div`, `dl`, `dt`, `fieldset`, `figcaption`, `figure`, `footer`, `form`, `h1`–`h6`, `header`, `hgroup`, `hr`, `li`, `main`, `nav`, `ol`, `p`, `pre`, `section`, `summary`, `menu`, `ul`, `br`, `table`, `caption`, `colgroup`, `col`, `thead`, `tbody`, `tfoot`, `tr`, `td`, `th`, `legend`, `optgroup` and `option`.

```orbit
<ul>
  <li>Posts by <b>{name}</b>
      and friends</li>
</ul>
```

renders `<ul><li>Posts by <b>Ana</b>⏎and friends</li></ul>` (⏎ is one newline).

**`"jsx"`** (Astro v7's default) applies JSX's rule (Babel's) to each text between two tags, expressions or blocks:

- every line is trimmed of spaces and tabs, except at the start of the first line and the end of the last;
- lines that are then empty are dropped;
- the remaining lines are joined with one space.

So whitespace that contains a newline disappears next to a tag or an expression, and whitespace within a line is kept. The example above renders `<ul><li>Posts by <b>Ana</b>and friends</li></ul>`: the words join. Write `{" "}` where a space must survive a line break:

```orbit
<li>Posts by <b>{name}</b>{" "}
    and friends</li>
```

**`false`** writes template text exactly as it appears.

## Expressions

`{expr}` takes any Go expression that produces one value: a variable, a field, a call, an operator expression, a composite literal or a conversion.

```orbit
<p>{props.Title}</p>
<p>{strings.ToUpper(name)}</p>
<p>{len(list)} posts</p>
<p>{map[string]int{"a": 1}["a"]}</p>
<p>{"}"} and {`{raw}`}</p>
```

Braces inside composite literals, strings, runes, raw strings and comments don't end the expression.

- Statements aren't expressions: `{x := 1}` is an error. Declare variables in the frontmatter.
- A call that returns several values (`posts.All()` returns a slice and an error) can't be used directly. Call it in the frontmatter and handle the error there.
- Expressions see everything the render body declares, plus `props`, `ctx`, `slots`, `locals`, `params` (in pages) and the `orbit` package. See [Components](../components.md#whats-available-in-the-render-body).

## Comments

| Syntax | Output |
|---|---|
| `{/* note */}` | Nothing. Use it for notes about the template. |
| `<!-- note -->` | Sent to the browser as an HTML comment, verbatim. `{…}` inside it isn't evaluated. |

## Control flow

### `{if}`

```orbit
{if len(list) == 0}
  <p>No posts</p>
{else if len(list) == 1}
  <p>One post</p>
{else}
  <p>{len(list)} posts</p>
{/if}
```

The header is copied into a Go `if` statement, so the condition must be a `bool` expression, and Go's error for a non-boolean condition is reported at the `.orbit` position. An init statement works too, as in Go, in `{if}` and `{else if}`: `{if n, ok := counts[tag]; ok}`. For the other places a template variable can live, see [Variables in the template](../components.md#variables-in-the-template).

### `{for}`

Any Go `for` clause works:

```orbit
{for i, p := range list}<li data-index={i}>{p.Title}</li>{/for}
{for range 3}<span>★</span>{/for}
{for i := 0; i < len(list); i += 2}<hr>{/for}
{for tag := range maps.Keys(counts)}<a href={"/tags/" + tag}>{tag}</a>{/for}
```

The last line ranges over an iterator (`maps.Keys` returns an `iter.Seq`). Each iteration has its own loop variables (Go 1.22 semantics), so closures that capture them are safe.

There's no `{else}` for an empty loop; wrap the loop in `{if len(list) > 0}` instead.

### Nesting rules

- A block must be closed inside the element where it was opened: `<ul>{for …}<li>…</li>{/for}</ul>` works, while `{if a}<div>{/if}</div>` is a parse error.
- Blocks can't appear inside a start tag. To set an attribute conditionally, use its value: `false` and `nil` omit an attribute (see [Attribute values](#attribute-values-on-html-elements)). To add several attributes at once, use a [spread](#spread-attributes).

## Elements

### HTML and custom elements

A tag that is lowercase or contains `-` is an HTML element or a custom element, written to the output as is (with its attributes rendered as described below).

- **Every element must be closed** explicitly. Orbit doesn't auto-close elements the way browsers do, so `<p>`, `<li>`, `<td>` and `<option>` all need their closing tag. A mismatched or missing closing tag is a parse error at its line and column:

  ```orbit file=src/pages/index.orbit expect-error
  <ul>
    <li>Unclosed
  </ul>
  ```

  ```text
  src/pages/index.orbit:3:1: unexpected </ul>: <li> opened at 2:3 isn't closed
  ```

- **Self-closing**: any element may be written self-closed, as in Astro. `<div />` renders `<div></div>`.

- **Void elements** take no closing tag: `area`, `base`, `br`, `col`, `embed`, `hr`, `img`, `input`, `link`, `meta`, `source`, `track`, `wbr`. Both `<br>` and `<br />` are accepted.

- **Raw-text elements**: the content of `<script>` and `<style>` is opaque. Orbit doesn't look for `{…}` there, so you can write JavaScript objects and CSS rules freely. `<title>` and `<textarea>` allow `{expr}`, but other markup inside them is text, as in HTML: `<title>a <b>bold</b> title</title>` renders the `<b>` literally.

- **Scripts and styles**: non-inline `<script>` and `<style>` tags anywhere in a template are extracted and bundled, and scoped styles add a `data-o-…` attribute to every element of the component. A `<script>` with any attribute other than a local `src`, such as `type="application/json"`, is left as it is. See [Styles and scripts](../styles-and-scripts.md).

### Components

A capitalized tag renders a component. Its name must be the alias of an `.orbit` import in the frontmatter:

```orbit
---
import Card "../components/Card.orbit"
---
<Card post={p} />
<Card post={p}><em>with children</em></Card>
```

A tag that matches no import alias is an error:

```orbit file=src/pages/index.orbit expect-error
---
import Card "../components/Card.orbit"
---
<Card post="hello" />
<Crad post="world" />
```

```text
src/pages/index.orbit:5:2: unknown component <Crad>: no .orbit import with this alias
```

Only `.orbit` files can be imported as components. A `.tsx`, `.jsx`, `.svelte` or `.vue` import is an error (`framework components aren't supported; use a .orbit component with a <script>`), and so is any other relative path that doesn't end in `.orbit`. See [Components](../components.md#no-framework-components).

Children of a component are passed as slots. A component with no children allocates nothing for slots. Children that are only whitespace pass no slot.

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

```orbit
<Panel title="Contact">
  <Fragment slot="actions">
    <a href="/rss.xml">RSS</a>
    <a href="/about">About</a>
  </Fragment>
</Panel>
```

As in Astro, `<Fragment>` takes only `slot`, `set:html` and `set:text`. `<Fragment set:html={body} />` writes raw HTML with no wrapper element. Any other attribute on a `<Fragment>` is a compile error.

### Doctype and HTML comments

`<!DOCTYPE html>` and `<!-- … -->` are copied to the output. Orbit inserts the page's CSS and JS links at the first `</head>`, or right after a leading doctype when there is no `</head>`.

## Attributes

| Form | On an HTML element | On a component |
|---|---|---|
| `a` | A bare attribute: `a` | `A: true` |
| `a="s"`, `a='s'` | A static value | `A: "s"` (a Go string literal) |
| `a=bare` | A static unquoted value, as in HTML | `A: "bare"` (a Go string literal, like a quoted value) |
| `a={expr}` | Rendered from the value (see below) | `A: expr` |
| `{a}` | Shorthand for `a={a}` | Shorthand for `a={a}` |
| `{...attrs}` | Spread of an `orbit.Attrs` | Not allowed |

Quoted values are literal text: `class="card {extra}"` renders `card {extra}`. To build a value, use an expression: `class={"card " + extra}`.

An expression value may contain `>`, quotes and braces; the scanner finds the closing `}` the same way as for `{expr}` (`featured={i > 0}` works).

### Attribute values on HTML elements

| Value | Output |
|---|---|
| `true` | The bare attribute: `<input disabled>` |
| `false`, `nil` | Nothing: the attribute is omitted. |
| a string | The escaped value in quotes. |
| `orbit.URL` | The escaped value in quotes, without URL filtering. |
| `orbit.HTML` | Escaped like any string; attribute values are never raw. |
| any other value | Formatted exactly as in [How values render](#how-values-render), then escaped and quoted. A slice concatenates its elements with no separator, so build lists such as class names yourself (`strings.Join(classes, " ")`). |

Because `false` omits the attribute, ARIA attributes that need the literal text `"false"` take a string: `aria-expanded={strconv.FormatBool(open)}`.

**URL attributes** are filtered at runtime. These are `action`, `background`, `cite`, `codebase`, `data`, `formaction`, `href`, `icon`, `longdesc`, `manifest`, `poster`, `profile`, `src`, `usemap`, `xmlns` and `xlink:href` (the list Go's `html/template` uses, plus `xlink:href`). If the value's scheme, after trimming leading whitespace and control characters and ignoring case, is `javascript:` or `vbscript:`, the value is replaced with `about:invalid#orbit`. Values of type `orbit.URL` skip the filter. The filter applies to expression values and to spread attributes; static values you write in the template are left as written.

HTML attribute names are written to the output exactly as you spell them; only component attributes are matched loosely (see [Name matching](#name-matching)).

### Spread attributes

`{...attrs}` writes every entry of an `orbit.Attrs` (`map[string]any`), sorted by name so the output is stable. Each value follows the rules above (`true` is bare, `false`/`nil` omitted, URL attributes filtered):

```orbit
<input {...props.Attrs} />
```

Each name must match `[A-Za-z_:][-A-Za-z0-9_:.]*`. A name with a space, quote, `>`, `/` or `=` is a render error, so a map built from user input can't inject markup. Spread works on HTML elements only.

Attributes are written in source order, with a spread's entries at the spread's position. Orbit doesn't merge duplicates: if a spread repeats an attribute written on the same element, both are written, and browsers use the first one.

## Directives

A directive is an attribute with a namespace (`set:`, `is:`, `server:`, `client:`) or the `slot` attribute. Orbit acts on it and doesn't write it to the output.

| Directive | Where | Effect |
|---|---|---|
| `set:html={expr}` | HTML element, `<Fragment>` | Sets the content to raw HTML. `expr` must have type `orbit.HTML`; a string constant written in the template is also accepted. A `string` variable is a compile error at the `.orbit` position. |
| `set:text={expr}` | HTML element, `<Fragment>` | Sets the content to the escaped text of `expr`. |
| `set:json={expr}` | `<script type="application/json">` only | Sets the content to `expr` encoded as JSON, escaped for a script element. See [`set:json`](#setjson). |
| `slot="name"` | A child of a component | Sends that child to the named slot instead of the default slot. |
| `is:inline` | `<script>`, `<style>` | Leaves the tag untouched: no extraction, bundling or scoping. |
| `is:global` | `<style>` | Skips CSS scoping. |
| `server:defer` | Component tag | Renders the component in a separate request after the page loads. See [`server:defer`](#serverdefer). |
| `client:load`, `client:idle`, `client:visible`, `client:media` | Component tag | Controls when the scripts of the component and its subtree load. See [`client:*`](#client). |

An element with a `set:` directive takes no children (whitespace aside); the directive replaces its content. `set:` directives on a component are a compile error.

The compiler **warns** (without failing) on `on*={expr}` attributes such as `onclick={…}` and on `style={expr}`: escaping makes a value safe in HTML, not in JavaScript or CSS. See [Escaping and security](../components.md#escaping-and-security).

### `set:json`

`set:json` passes server data to client scripts. It's only valid on a `<script type="application/json">`, which Orbit never processes or bundles:

```orbit
<script type="application/json" id="tags" set:json={props.Tags}></script>
```

- `expr` can have any type. It's encoded with `encoding/json`, so `json` struct tags apply, and an encoding error is a render error at the `.orbit` position.
- The output is escaped for the script element: `<`, `>`, `&`, U+2028 and U+2029 become `\u003c`, `\u003e`, `\u0026`, `\u2028` and `\u2029`. A value containing `</script>` or `<!--` can't end the element.
- A script reads it with `JSON.parse(document.getElementById("tags").textContent)`.
- On any other element, `set:json` is a compile error: `set:json only works on <script type="application/json">`.

For per-element data, `data-*` attributes are escaped like any attribute and read with `el.dataset`. Both patterns, with a custom-element example, are in [Components](../components.md#passing-data-to-client-scripts).

### `server:defer`

`server:defer` is a bare attribute on a component tag (`<Self>` included). Instead of rendering the component, the page writes a placeholder with the component's `fallback` slot, and the browser fetches the component's HTML after the page loads:

```orbit
<Comments server:defer postID={post.Slug}>
  <p slot="fallback">Loading comments…</p>
</Comments>
```

- Props are passed as usual, and every field of the component's `Props` must be JSON-serializable; that's checked when Orbit generates code.
- The only allowed children are elements or `<Fragment>`s with `slot="fallback"`, plus whitespace. With no fallback, the placeholder is empty.

| Mistake | Compile error |
|---|---|
| Any other child | `only slot="fallback" content is allowed inside a server:defer component` |
| On an HTML element, `<slot>` or `<Fragment>` | `server:defer only works on .orbit components` |
| With a value (`server:defer="x"`) | `server:defer takes no value` |

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
| On an HTML element, `<slot>` or `<Fragment>` | `client:visible only works on .orbit components; move the <script> into a component` |
| An expression value (`client:visible={margin}`) | `client:visible takes a static string value, not an expression` |
| A value on `client:load` or `client:idle` | `client:idle takes no value` |
| `client:media` without a query | `client:media needs a media query, e.g. client:media="(max-width: 600px)"` |
| Two `client:*` directives on one tag | `only one client:* directive is allowed per component` |
| `client:only` | `client:only is for framework components, which Orbit doesn't support; use client:load, client:idle, client:visible or client:media` |

The messages name the directive you wrote. If a component's scripts also load eagerly elsewhere on the same page, they stay eager. See [Lazy loading](../lazy-loading.md#client-lazy-scripts) for the details.

### Combining and reserved namespaces

`server:defer` and a `client:*` directive on the same tag is a compile error:

```orbit file=src/pages/blog/[slug].orbit expect-error
---
import Card "../components/Card.orbit"
---
<Card server:defer client:idle />
```

```text
src/pages/blog/[slug].orbit:4:7: server:defer and client:* can't be combined; put client:* on a component inside the deferred one
```

The `server:` and `client:` namespaces are reserved. An unknown directive in either is a compile error at the attribute, listing the valid ones:

```orbit file=src/pages/index.orbit expect-error
---
import Card "../components/Card.orbit"
---
<Card client:hover />
<Card server:lazy />
```

```text
src/pages/index.orbit:4:7: unknown directive "client:hover" (client directives: client:load, client:idle, client:visible, client:media)
src/pages/index.orbit:5:7: unknown directive "server:lazy" (server directives: server:defer)
```

Astro's `class:list` and `define:vars` aren't supported, and using them is a compile error with a hint, rather than being written out as plain attributes:

```orbit file=src/components/Card.orbit expect-error
---
type Props struct {
    Featured bool
}
---
<article class:list={[]string{"card"}}>
  <h2>Hello</h2>
</article>
<script define:vars={props}>
  console.log("card");
</script>
```

```text
src/components/Card.orbit:6:10: class:list isn't supported; build the class string in the frontmatter (e.g. strings.Join)
src/components/Card.orbit:9:9: define:vars isn't supported; pass data with data-* attributes or set:json
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

When Orbit can't list the target's fields from its source, because `Props` embeds another struct or is an alias (`type Props = other.T`), it converts the attribute to PascalCase, writing each word found in Go's common-initialism table in capitals: `user-id` becomes `UserID`, `api-url` becomes `APIURL`. The table is the one Go linters use: `ACL`, `API`, `ASCII`, `CPU`, `CSS`, `DNS`, `EOF`, `GUID`, `HTML`, `HTTP`, `HTTPS`, `ID`, `IP`, `JSON`, `LHS`, `QPS`, `RAM`, `RHS`, `RPC`, `SLA`, `SMTP`, `SQL`, `SSH`, `TCP`, `TLS`, `TTL`, `UDP`, `UI`, `UID`, `UUID`, `URI`, `URL`, `UTF8`, `VM`, `XML`, `XMPP`, `XSRF`, `XSS`.

A page's route params get their `params` field names from this PascalCase conversion: `[post-id]` becomes `params.PostID`, `[id]` becomes `params.ID` (see [Route params](../routing.md#field-names)).

### Values

- `a="s"` and `a=bare` become the Go string literal `"s"`, so they only fit a `string` field (or a named string type).
- A bare `a` becomes `true`.
- `a={expr}` passes `expr` as is; the Go compiler checks its type against the field.
- A field you don't set has its zero value.

### Unknown props

An attribute that matches no field is reported by Orbit at the attribute name, in your spelling, with the names that would work (in lower camel case, such as `isOpen`). With the `PostMeta` component of the [example](#example):

```orbit file=src/pages/index.orbit expect-error
---
import PostMeta "../components/PostMeta.orbit"
---
<PostMeta titel="Hello, Orbit" />
```

```text
src/pages/index.orbit:4:11: unknown prop "titel" on <PostMeta> (props: title, url, date, tags, draft, attrs)
```

When Orbit can't list the fields (the embed and alias case above), it skips this check. Go then reports the unknown field, still at the attribute name, with the type name rewritten to your alias. With this `Card`, whose `Props` embeds a struct:

```orbit file=src/components/Card.orbit
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

```orbit file=src/pages/index.orbit expect-error
---
import Card "../components/Card.orbit"
---
<Card psot="hello" />
```

```text
src/pages/index.orbit:4:7: unknown field Psot in struct literal of type Card.Props
```

## How values render

`{expr}` writes its value according to the value's type. Orbit picks the write at compile time from the static type, and falls back to a runtime check for interface values; both give byte-identical output.

| Type | Renders as | Example → output |
|---|---|---|
| untyped `nil`, or a nil interface value | nothing | `{nil}` → `` |
| `string` | escaped text | `{"a < b"}` → `a &lt; b` |
| `orbit.HTML` | raw, unescaped | `{orbit.HTML("<b>hi</b>")}` → `<b>hi</b>` |
| `orbit.URL` | escaped text | `{orbit.URL("/a?b=1&c=2")}` → `/a?b=1&amp;c=2` |
| `[]byte` | escaped text, not a list of numbers | `{[]byte("hi")}` → `hi` |
| `int`, `int8`…`int64`, `rune` | decimal | `{42}` → `42`, `{'a'}` → `97` |
| `uint`, `uint8`…`uint64`, `byte` | decimal | `{byte(65)}` → `65` |
| `float64` | shortest form, as `fmt.Sprint` | `{0.1}` → `0.1`, `{2.0}` → `2`, `{1e6}` → `1e+06` |
| `float32` | shortest float32 form | `{float32(0.1)}` → `0.1` (not `0.10000000149011612`) |
| `bool` | `true` or `false` | `{ok}` → `true` |
| `error` | its escaped `Error()` text; a nil error renders nothing | `{err}` → `file not found` |
| `fmt.Stringer` | its escaped `String()` text | `{time.Second}` → `1s` |
| `orbit.Renderer`, `func(*orbit.Writer) error` | called; whatever it writes is output | `{slots.Get("footer")}` |
| a slice of the above | each element in order, no separator | `{[]string{"a", "b"}}` → `ab` |
| anything else | `fmt.Sprint`, escaped | `{struct{ X int }{1}}` → `{1}` |

Details that are easy to trip over:

- **Characters are numbers**: a `rune` or `byte` prints its code (`{'a'}` → `97`). Use `{string(r)}` for the character.
- **Named types** print like their underlying value, the way `fmt.Sprint` prints them: `type Slug string` prints the string, `type Count int` the number. If the type has a `String` method, that's used instead.
- **Only `orbit.HTML` itself is raw.** A type defined from it (`type Markup orbit.HTML`) is escaped like a string, and a type defined from `orbit.URL` is URL-filtered like a string. Convert back with `orbit.HTML(m)` to write raw.
- **`String` on a pointer receiver** is only used when the value is a pointer: a `T` value whose `String` is declared on `*T` prints with `fmt.Sprint` (its fields), not with `String`.
- **Pointers aren't dereferenced**: a `*string` prints an address. Write `{*p}`, after checking `p != nil`.
- **Floats** use the shortest exact form, which switches to exponent notation for large and small values (`1e+06`, `1e-07`). For a fixed format, call `strconv.FormatFloat` or `fmt.Sprintf("%.2f", x)`.
- **`time.Time`** is a `fmt.Stringer`, so `{date}` prints `2026-10-01 00:00:00 +0000 UTC`. Use `{date.Format("2 Jan 2006")}` for a readable date.

Attribute values follow the same formatting, with the attribute rules for `true`, `false`, `nil` and URLs on top (see [Attribute values](#attribute-values-on-html-elements)).

## See also

- [Components](../components.md): frontmatter, props, slots, recursion, passing data to scripts and escaping, with examples
- [Lazy loading](../lazy-loading.md): `server:defer` and `client:*` in depth
- [Styles and scripts](../styles-and-scripts.md): `<style>`, `<script>`, `is:inline` and `is:global`
- [Runtime API](runtime-api.md): `orbit.HTML`, `orbit.URL`, `orbit.Attrs`, `orbit.Slots`, `orbit.Renderer`
- [CLI](cli.md): where errors and warnings are reported
