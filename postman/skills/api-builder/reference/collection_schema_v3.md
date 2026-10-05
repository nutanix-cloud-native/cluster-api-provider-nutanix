## V3 YAML Schema

**Definition file (`.resources/definition.yaml`):**
The definition contains metadata for a collection or folder. It is optional.
- $kind: "collection" (required for both collection and folder definitions)
- name: string (optional, defaults to the filesystem folder name)
- description: string (optional)
- variables: array of {key, value, description?, disabled?} (optional)
  - value must be a string
  - disabled: boolean
- auth: single auth object OR array of auth configs (multiAuth) (optional)
  - Single: {type, credentials: [{key, value}, ...]}
  - Multi: [{id, name, type, credentials, rules?}, ...]
- scripts: array of {type, code, language: "text/javascript"} (optional)
  - Types: "http:beforeRequest", "http:afterResponse", "graphql:beforeQuery", "graphql:afterResponse", "grpc:beforeInvoke", "grpc:onIncomingMessage", "grpc:afterResponse"
- order: number (optional, used for folder ordering)

**HTTP Request:**
- $kind: "http-request" (required)
- name: string (optional)
- order: number (optional) -> used to decide request position; value is only for comparison, and keeping values spaced out (for example by 1000) is preferred
- url: string (with variable syntax: {{varName}})
- method: GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS
- headers: array of {key, value, description?, disabled?}
- queryParams: array of {key, value, description?, disabled?}
  - must match the url's query string: when non-empty, queryParams replaces it at run time, and `postman collection lint` errors (FMT209) when they differ
- pathVariables: array of {key, value, description?}
- body: {type, content} (type is required when body is present)
  - Types: "json", "formdata", "urlencoded", "text", "xml", "html", "javascript", "file", "none"
  - json/text/xml/html/javascript: content is string
  - formdata: content is array of {key, type: "text"|"file", value or src, contentType?, description?}
  - urlencoded: content is array of {key, value, description?}
- auth: {type, credentials}
- settings: {protocolVersion?, strictSSL?, followRedirects?, maxRedirects?, disabledSystemHeaders?}
- scripts: array of {type: "beforeRequest"|"afterResponse", code, language: "text/javascript"}
- examples (optional): relative path to examples directory. Example: `./.resources/<request-name>.resources/examples/`

**HTTP Example:**
- $kind: "http-example" (required)
- name: string (optional)
- request:
    url: string
    method: string
- response:
    statusCode: number
    statusText: string
    headers: array of {key, value}
    body: {type, content}
- order: number (optional)

**GraphQL Request:**
- $kind: "graphql-request" (required)
- url: string
- order: number (optional)
- query: string (GraphQL query)
- variables: string (YAML string containing JSON object)
- headers: array of {key, value, description?, disabled?}
- auth: {type, credentials}
- settings: {disabledSystemHeaders?}
- scripts: array of {type: "beforeQuery"|"afterResponse", code, language}

**gRPC Request:**
- $kind: "grpc-request" (required)
- url: string
- order: number (optional)
- methodPath: string
- methodDescriptor: string
- message: {content: string (JSON)}
- metadata: array of {key, value, description?}
- auth: {type, credentials}
- settings: {secureConnection?, strictSSL?, maxResponseMessageSize?, includeDefaultFields?, connectionTimeout?}
- scripts: array of {type: "beforeInvoke"|"afterResponse", code, language}

**WebSocket Request:**
- $kind: "websocket-request" (required)
- url: string
- order: number (optional)
- headers: array of {key, value, description?, disabled?}
- queryParams: array of {key, value, description?, disabled?} (must match the url's query string)
- settings: {handshakeTimeout?, retryCount?, retryDelay?, maxPayload?, strictSSL?}

**Socket.IO Request:**
- $kind: "socket.io-request" (required)
- url: string
- order: number (optional)
- headers: array of {key, value}
- queryParams: array of {key, value} (must match the url's query string)
- events: array of {name, description?, subscribeOnConnect: boolean}
- settings: {version?, path?, handshakeTimeout?, retryCount?, retryDelay?, strictSSL?}

**MQTT Request:**
- $kind: "mqtt-request" (required)
- url: string
- order: number (optional)
- clientId: string
- version: 4 | 5
- topics: array of {name, qos: 0|1|2, subscribe: boolean, description?, settings: {noLocal?, retainAsPublished?, retainHandling?, subscriptionIdentifier?}}
- lastWill: {topic, payload, qos, retain, type: "text"|"json", properties: {messageExpiryInterval?, contentType?}}
- properties: {sessionExpiryInterval?, receiveMaximum?, maximumPacketSize?, requestResponseInformation?, userProperties: [{key, value}]}
- settings: {cleanSession?, keepAlive?, autoReconnect?, connectionTimeout?, strictSSL?}

**MCP Request:**
- $kind: "mcp-request" (required)
- transport: "sse" | "stdio"
- order: number (optional)
- SSE: {url, headers?, message, auth?, settings: {strictSSL?, requestTimeout?, sessionTimeout?}}
- STDIO: {command, env: [{key, value}], message, auth?, settings: {requestTimeout?}}

**LLM Request:**
- $kind: "llm-request" (required)
- url: string
- order: number (optional)
- config: {model, provider}
- userPrompts: array of {id, value, timestamp, active, type: "text"}
- systemPrompts: array of {id, value, timestamp, active, type: "text"}
- mcpConfig: string (optional, JSON config)
- enabledTools: array of strings (optional)
- auth: {type, credentials}
- settings: {temperature?, maxToken?, streamResponse?, responseFormatJSON?, topP?, presencePenalty?, frequencyPenalty?, maxSteps?, streamTools?}


Environments are stored in the postman/environments/ folder as
`<name>.environment.yaml` files. The `.environment` part of the suffix is required for
`workspace push` to pick the file up as an environment. `.environment.yml` and
`.environment.json` are also accepted; prefer `.environment.yaml`.
After writing one by hand, confirm the directory with
`postman environment lint postman/environments`.

The environment YAML format structure:

**Environment Root:**
- name: string (required)
- values: array of {key, value: string, enabled, type}
  - value must be a string (same rule as collection variables)
  - enabled: boolean
  - type: string (e.g. "default")

User asks: "Create a development environment"

YAML RULES (CRITICAL - invalid YAML breaks parsing, when in doubt single-quote it):
1. Single-quote values with {{variables}}: `url: '{{base_url}}/users'` (never unquoted).
2. Single-quote values containing `: # & * ! [ ] { } > |`, for example: `name: 'Health check: v2'`.
3. Multi-line content (JSON bodies, scripts, queries) MUST use `|-` block scalar:
   body:
     type: json
     content: |-
       {
         "name": "example"
       }
4. Quote strings resembling booleans/numbers when intended as strings: `value: "true"`, `value: "123"`.
5. `order` field MUST be a bare number, never quoted: `order: 1000`.
6. Single-quote file paths and use forward slashes only: `examples: './.resources/name.resources/examples'`.

Naming rules:
- `<request-name>` (filename stem before `.request.yaml`) must NOT contain `/ \ : * ? " < > |`; sanitize to `-`.
- `<environment-name>` (filename stem before `.environment.yaml`) follows the same
  sanitization as a request name. Write `<name>.environment.yaml`, not `<name>.yaml`.
- Include `name` only when it differs from `<request-name>` (for example, `name: 'Health/check'` in `Health-check.request.yaml`).
- Filenames must be unique (case-insensitive) per directory.
- Never place request files inside `.resources/` directories.

Schema example ("bookstore api"):

`postman/collections/bookstore api/get all books.request.yaml`
```yaml
$kind: http-request
method: GET
url: '{{base_url}}/books'
order: 1000
```

`postman/collections/bookstore api/get-book-by-id.request.yaml`
```yaml
$kind: http-request
name: 'get book by :id'
method: GET
url: '{{base_url}}/books/:id'
order: 2000
pathVariables:
  - key: id
    value: '1'
```

`postman/collections/bookstore api/add new book.request.yaml`
```yaml
$kind: http-request
method: POST
url: '{{base_url}}/books'
order: 3000
headers:
  - key: Content-Type
    value: application/json
body:
  type: json
  content: |-
    {
      "title": "Example Book",
      "author": "Jane Doe"
    }
```

`postman/collections/bookstore api/.resources/definition.yaml`
```yaml
$kind: collection
name: Bookstore API
variables:
  - key: base_url
    value: 'https://api.bookstore.com/v1'
```

## Directory Structure (File System Mode)

When Postman is in File System mode, collections are stored in `postman/collections/` using the directory structure below.

Every folder in `postman/collections/` represents a collection. A collection can contain subfolders and requests.

A folder or collection can have a `.resources/` directory, which is an optional metadata/resources directory for that scope. The optional definition file `.resources/definition.yaml` stores collection/folder metadata. Request examples are stored in `.resources/<request-name>.resources/`.

Example directory tree for a collection called "bookstore api":
```text
postman/collections/
  bookstore api/
    .resources/
      definition.yaml (optional)
      get all books.resources/
        examples/
          200 OK.example.yaml
          400 Bad Request.example.yaml
          500 Internal Server Error.example.yaml
    get all books.request.yaml
    get-book-by-id.request.yaml
    add new book.request.yaml
    authentication/
      .resources/
        definition.yaml (optional)
      signup.request.yaml
      login.request.yaml
```

Note: Every entity (request, collection, folder, example) has its own separate file.
