# 002. Motor v0.2.0 seguro y extensible

## Objetivo

Ampliar el subconjunto útil para Lilith y otros consumidores sin convertir el
motor en un Bash incompleto e impredecible. La versión conserva los defaults de
v0.1.0 y añade capacidades mediante API aditiva y opt-in cuando existe riesgo de
ampliar la superficie de entrada.

## Lenguaje

- `case`, brace expansion, backticks e IFS configurable;
- eliminación y sustitución de patrones en parámetros;
- here strings y heredocs acotados; los heredocs requieren
  `AllowHeredocs: true`;
- descriptores virtuales entre 0 y 255, con herencia de archivos reales para
  procesos Unix;
- builtins `source`, `local`, `readonly`, `trap`, `getopts`, `umask`, `exec`,
  `hash` y `times`;
- opciones `errexit`, `noglob`, `nounset`, `xtrace` y `pipefail`.

Siguen fuera de alcance las sesiones interactivas, job control, arrays, process
substitution, `[[ ]]`, coprocesses y la conformidad total Bash/POSIX.

## API de embedding

- `Parse`, `Program`, `RunProgram` y `Check` separan validación de ejecución;
- `Snapshot`, `Restore` y `Clone` permiten sesiones reproducibles e
  independientes;
- `Policy` autoriza comandos expandidos y redirecciones antes de sus efectos;
- `Handlers` crea una cadena ordenada además del handler compatible de v0.1;
- `Observer` publica eventos estructurados síncronos;
- `ExternalDisabled`, `RootDir`, `SourceLoader` y `FileSystem` permiten reducir
  la superficie disponible a un script;
- errores tipados distinguen sintaxis, features, expansión, política, límites,
  redirects, comandos y estado.

Estos controles son guardrails para el host. No sustituyen un sandbox de
sistema operativo, especialmente si se permiten ejecutables externos.

## Límites

Cada ejecución tiene presupuesto independiente para tamaño de script y AST,
comandos, iteraciones, ancho de pipeline, globbing, archivos abiertos,
profundidad de funciones y `source`, brace expansion, command substitution,
heredocs y stdout/stderr.

Los defaults son finitos salvo el output superior, que permanece ilimitado para
compatibilidad. Un valor negativo desactiva el límite correspondiente cuando la
API lo documenta.

## Procesos y plataformas

- Unix conserva process groups y cancelación de descendientes.
- Windows resuelve `.cmd`/`.bat` mediante `COMSPEC` y usa Job Objects con
  `KILL_ON_JOB_CLOSE` para cancelar árboles de procesos.
- Windows rechaza explícitamente la herencia de descriptores host mayores que
  2; builtins y handlers conservan la tabla virtual completa.
- El módulo continúa sin dependencias, sin CGO y con builds para Linux,
  Windows, macOS y Android.

## Validación requerida antes de publicar

- formato, suite completa, race detector, vet y build estático;
- cobertura mínima automatizada;
- fuzzing independiente de parser, brace expansion y quoting;
- comparación diferencial del subconjunto compartido contra `sh`;
- tests nativos en Linux, Windows y macOS;
- compilación cruzada de tests para Windows amd64, macOS arm64 y Android arm64;
- ejemplos compilables y verificación de módulo sin dependencias.
