# Contexto maestro de go-portable-shell

## Objetivo

Motor de shell no interactivo, escrito desde cero en Go puro, para sustituir el
intérprete de terceros usado por Lilith Code en su fallback portable.

## Alcance

- Subconjunto Bash/POSIX explícito y estable.
- Parser, expansión, ejecución, pipelines y control de flujo propios.
- Builtins y procesos externos.
- API pública de handlers para toolboxes integrados.
- Cancelación por contexto y límites de sustitución.
- Sin dependencias, sin CGO y con licencia 0BSD.

## Fuera de alcance

No se promete conformidad Bash completa. Heredocs, job control, arrays,
process substitution y extensiones complejas deben rechazarse claramente.

## Consumidor inicial

Lilith Code importará `github.com/YahirHub/go-portable-shell` y conservará sus
comandos Go portables mediante `Config.Handler`.
