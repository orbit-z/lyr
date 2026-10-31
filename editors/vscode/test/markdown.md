# Lyr in Markdown

```lyr
---
import Card "../components/Card.lyr"

title := "Hi"
---
<Card title={title}>
  {if props.Show}<p class="x">{title}</p>{/if}
</Card>
<style>
  p { color: red; }
</style>
```

  ```Lyr file=src/pages/index.lyr
  <h1>{title}</h1>
  ```

~~~lyr
<p>no frontmatter</p>
~~~

```lyr
<script>
  let open = 1;
```

```go
x := 1
```

After the fences.
