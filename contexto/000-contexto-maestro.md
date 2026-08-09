# Contexto maestro de go-portable-shell

## Objetivo

Motor de shell no interactivo, escrito desde cero en Go puro, para sustituir el
intérprete de terceros usado por Lilith Code en su fallback portable.

## Alcance

- Subconjunto Bash/POSIX explícito y estable.
- Parser, expansión, ejecución, pipelines y control de flujo propios.
- Builtins y procesos externos.
- API pública de programas, handlers, políticas, observadores y snapshots.
- Cancelación por contexto, límites configurables y filesystem sustituible.
- Sin dependencias, sin CGO y con licencia 0BSD.

## Fuera de alcance

No se promete conformidad Bash completa. Job control, arrays, process
substitution y extensiones complejas deben rechazarse claramente. Los heredocs
son una extensión acotada, desactivada por defecto y con opt-in explícito.

## Consumidor inicial

Lilith Code importará `github.com/YahirHub/go-portable-shell` y conservará sus
comandos Go portables mediante `Config.Handler`.

## Historial técnico

- `001-motor-inicial-v0-1-0.md`: primera implementación estable.
- `002-motor-v0-2-0-seguro-extensible.md`: lenguaje ampliado, controles de
  embedding, límites, portabilidad de procesos y validación reforzada.
