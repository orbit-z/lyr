# Orbit in Markdown

```orbit
---
import Card "../components/Card.orbit"

title := "Hi"
---
<Card title={title}>
  {if props.Show}<p class="x">{title}</p>{/if}
</Card>
<style>
  p { color: red; }
</style>
```

  ```Orbit file=src/pages/index.orbit
  <h1>{title}</h1>
  ```

~~~orbit
<p>no frontmatter</p>
~~~

```orbit
<script>
  let open = 1;
```

```go
x := 1
```

After the fences.
