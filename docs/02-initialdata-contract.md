# The initialData Contract

## What it is

`initialData` is the data that Go serializes and passes to React at render time. It flows like this:

```
Go DB query → Go struct → JSON → window.__APP_DATA__.initialData → SSRContext → component
```

The same data travels two paths simultaneously:
1. **SSR path**: Go passes it to `callSSR()` → Node SSR sidecar → `render()` in `entry-server.tsx` → `SSRProvider` → component renders with real data (no loading state)
2. **Hydration path**: Go serializes it to `window.__APP_DATA__` in the `<script>` tag → `main.tsx` reads it → `SSRProvider` → `hydrateRoot()` sees matching props → silent hydration

## The constraint

There is **no compile-time enforcement** across the Go↔React boundary. You can rename a field in your Go struct and the TypeScript will compile fine — but React will silently receive `undefined` for that field, causing loading states to appear even on SSR pages.

## How to define the contract

### Go side (cmd/web/main.go)

```go
// Define your initialData struct.
// All fields must be exported (capitalized) for JSON serialization.
type PostInitialData struct {
    ID          string `json:"id"`
    Title       string `json:"title"`
    Body        string `json:"body"`
    CoverURL    string `json:"coverUrl"`
    AuthorName  string `json:"authorName"`
    PublishedAt string `json:"publishedAt"`
}

func buildPostInitialData(post db.Post) PostInitialData {
    return PostInitialData{
        ID:          post.ID,
        Title:       post.Title,
        Body:        post.Body,
        CoverURL:    post.CoverImageURL,
        AuthorName:  post.Author.Name,
        PublishedAt: post.PublishedAt.Format(time.RFC3339),
    }
}
```

### React side (SSRContext.tsx and your component)

```typescript
// Match the Go struct field-for-field.
// JSON field names (the json:"..." tags) map to TypeScript field names.
interface PostInitialData {
  id: string
  title: string
  body: string
  coverUrl: string | null
  authorName: string
  publishedAt: string
}

// In your component:
const { initialData } = useSSRContext()
const post = initialData as PostInitialData
// Use post.title, post.body, etc. — available immediately on first render
```

## Field name mapping

Go's JSON encoding uses the `json:""` struct tag. If no tag is set, it uses the field name with the first letter lowercased. Always use explicit tags to be clear:

| Go field | Go tag | TypeScript field |
|---|---|---|
| `Title string` | `json:"title"` | `title: string` |
| `CoverURL string` | `json:"coverUrl"` | `coverUrl: string` |
| `PublishedAt time.Time` | `json:"publishedAt"` | `publishedAt: string` |

Note: Go `time.Time` serializes to RFC 3339 string — TypeScript receives a `string`, not a `Date`.

## Detecting mismatches

A mismatch between Go and React `initialData` shapes produces one of these symptoms:

1. **Component shows "Loading..." even on SSR pages** — the component is checking `if (!data)` and sees `undefined` for a field it expected. The SSR render produced an empty/loading state, which was cached and served.

2. **React hydration warning in browser console** — "Warning: Text content did not match. Server: '...' Client: '...'" This means the SSR render and client render produced different HTML. The page still works but there was a re-render flash.

3. **Data appears in the page source but not in the rendered page** — the field is in `window.__APP_DATA__` but the TypeScript is reading a different field name.

### Debugging checklist

1. Open the page source (Cmd+U). Find `window.__APP_DATA__`. Check that the JSON contains the expected fields with the expected values.
2. In browser DevTools console: `window.__APP_DATA__` — inspect the object.
3. Check the React component: is it reading `initialData.fieldName` and is `fieldName` spelled exactly as it appears in the JSON?
4. Check the Go struct: does the `json:"..."` tag match the TypeScript field name?

## Recommended: JSON schema validation in CI

The contract is informal unless you enforce it. A simple approach:

1. Write a JSON Schema file that describes your `initialData` shape.
2. In Go tests: serialize a sample struct and validate against the schema.
3. In frontend tests: import the schema and validate a mock `initialData` object.
4. Add both checks to CI.

This catches mismatches at PR time rather than production.
