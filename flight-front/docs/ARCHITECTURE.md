# Flight-front

```mermaid
flowchart LR
  User[Persian RTL user] --> Form[search form]
  Form --> Proxy[Nginx /api proxy]
  Proxy --> Core[core-api]
  Core --> Cards[result cards]
  Cards --> Compare[three-flight comparison]
  Cards --> Book[provider booking link]
```

The frontend owns Persian formatting, ranking-mode selection, stop/airline
filters, loading/error states, comparison, freshness/deal explanations, and
provider links. It does not call Elasticsearch, PostgreSQL, or ranker directly.
