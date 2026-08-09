# 001. Motor inicial v0.1.0

## Arquitectura

- `lexer.go`: palabras, quotes, operadores, redirecciones y expansiones.
- `parser.go`/`ast.go`: AST de listas, pipelines, control de flujo y funciones.
- `expand.go`/`arithmetic.go`: parámetros, sustitución, fields, globbing y cálculo.
- `execute.go`: estado, control de flujo, pipelines concurrentes y redirects.
- `builtins.go`: builtins no interactivos con errores explícitos.
- `external.go`/`process_*.go`: resolución, ejecución y cancelación de procesos.
- `types.go`: API pública, configuración, handlers, errores y estados.

## Decisiones

- No depender de otro lexer o intérprete evita trasladar el mismo problema de
  licencia y mantiene un módulo auditable.
- El contrato es un subconjunto, no Bash completo. Toda construcción reconocida
  pero fuera de alcance debe fallar antes de ejecutar efectos parciales.
- `CommandHandler` permite que Lilith conserve su toolbox sin acoplarlo al core.
- `Runner` conserva ambiente/directorio/funciones entre llamadas secuenciales;
  las pipelines y subshells clonan estado para aislar mutaciones.
- La sustitución de comandos tiene 1 MiB de límite predeterminado y la expansión
  se limita a 32 niveles.
- En Unix los ejecutables externos usan un process group propio para cancelar
  descendientes junto con el contexto.

## Validación v0.1.0

- suite funcional del runner: variables, quotes, pipeline, handler, redirección,
  funciones, `if`, `while`, `until`, `for`, subshell, parámetros, `$()`,
  aritmética, glob, `pipefail`, lectura, posiciones y estados;
- `go test ./...`;
- `go test -race ./...`;
- `go vet ./...`;
- `CGO_ENABLED=0 go build ./...`;
- compilación de tests para Windows amd64;
- compilación/ejecución simulada para Android arm64;
- fuzzing del parser: más de 94 mil ejecuciones en dos segundos, sin panic.

El primer push a `YahirHub/go-portable-shell` crea automáticamente el tag y el
release `v0.1.0` si todavía no existen. Los pushes siguientes son no-op para esa
versión; las versiones posteriores requieren su propio tag deliberado.
