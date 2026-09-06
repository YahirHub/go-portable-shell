# 003. Shell universal v0.3.0

## Objetivo

Convertir el motor portable en una opción viable como shell no interactiva
predeterminada para CLIs de agentes y herramientas similares a Claude, sin
seleccionar otra shell por detectar Git en Windows.

La estrategia de v0.3.0 no consiste en emular `cmd.exe`, PowerShell o Bash. El
motor mantiene su lenguaje acotado y añade una caja de herramientas propia en
Go puro para que comandos Linux frecuentes tengan el mismo punto de entrada en
Windows, Linux, macOS y Android.

## Resolución de comandos

El orden de un comando simple queda definido así:

1. función de shell;
2. builtin del lenguaje;
3. `Config.Handler`;
4. `Config.Handlers` en orden;
5. utilidad portable de v0.3;
6. ejecutable del sistema operativo.

Conservar los handlers antes de las utilidades evita romper el toolbox de
Lilith u otro host que ya implemente comandos llamados `grep`, `ls`, etc. Las
utilidades sí preceden al ejecutable externo, por lo que la shell no depende de
Git Bash para disponer de sus herramientas básicas.

Un path de ejecutable explícito, por ejemplo `/usr/bin/grep`, no coincide con
el nombre de una utilidad portable y continúa al resolver externo.

## Utilidades Linux portables

Se agregan implementaciones sin dependencias para:

- filesystem: `ls`, `mkdir`, `rmdir`, `rm`, `cp`, `mv`, `touch`, `chmod`;
- texto: `cat`, `head`, `tail`, `wc`, `grep`;
- descubrimiento: `find`, `which`, `basename`, `dirname`;
- entorno y sistema: `env`, `printenv`, `sleep`, `uname`, `whoami`.

Se priorizan opciones usadas por automatización y agentes, entre ellas
`ls -la`, `mkdir -p`, `rm -rf`, `cp -R`, `head -n`, `tail -n`, combinaciones de
`wc`, `find -name/-iname/-type/-mindepth/-maxdepth` y
`grep -i/-n/-v/-q/-c/-l/-L/-r/-R/-F/-E`.

No se promete compatibilidad GNU/BSD completa. Opciones no implementadas fallan
con estado de uso en vez de reinterpretarse. El modo regexp de `grep` usa RE2 de
Go y se documenta como tal.

## Filesystem, root y seguridad

Las utilidades resuelven rutas mediante `Runner.resolvePath`, por lo que
`RootDir` sigue aplicando. Lecturas y metadatos pasan por `Config.FileSystem`.

El contrato requerido `FileSystem` no se amplía, para no romper implementaciones
existentes. `OSFileSystem` agrega métodos opcionales de `ReadDir`, creación,
borrado, rename, cambio de tiempos y chmod. Las utilidades detectan esas
capacidades por interfaces internas; un filesystem personalizado que no las
implemente obtiene un error explícito para la operación correspondiente.

Los archivos abiertos por las utilidades consumen el presupuesto de
`MaxOpenFiles` y los bucles de lectura/recorrido consultan `context.Context`.

Se agregan defensas específicas contra copiar un archivo sobre sí mismo y mover
un path sobre sí mismo o dentro de sí mismo.

## Traducción de comandos del host

La traducción solo se intenta si el nombre original no existe en `PATH`. Esto
mantiene prioridad para instalaciones nativas reales.

### Windows

- `apt` y `apt-get` tienen un subconjunto acotado hacia `winget`:
  - `update` -> `winget source update`;
  - instalación de un solo paquete -> `winget install`;
  - `upgrade`/`full-upgrade` -> `winget upgrade`;
  - eliminación de un solo paquete -> `winget uninstall`;
  - `search`, `show` y `list` -> equivalentes directos de `winget`.
- `-y` agrega opciones no interactivas y aceptación de acuerdos soportadas por
  `winget`.
- `python3` prueba `python` y después `py -3`.
- `pip3` prueba `pip` y después `py -3 -m pip`.
- `xdg-open` puede caer a `rundll32.exe` con el handler de URL/archivo.

Formas ambiguas, como una instalación de varios paquetes que no tenga una
traducción inequívoca, permanecen sin resolver en lugar de adivinar.

### macOS

Cuando `apt`/`apt-get` no existen se traduce el mismo subconjunto hacia Homebrew.
`xdg-open` puede caer a `open`.

### Linux y Android

No se reescriben los comandos de package manager; se conserva el ejecutable
nativo.

Si una traducción cambia el proceso que se ejecutará, `Policy.CheckCommand`
vuelve a autorizar los argumentos traducidos. Los eventos externos muestran el
comando efectivo.

## Operadores y composición

El parser existente ya soportaba listas por newline/`;`, `&&`, `||`, `!` y
pipelines. v0.3 conserva ese AST y lo valida junto con las nuevas utilidades. Por
ejemplo:

```sh
mkdir -p build && cp -R src build/src; find build -type f -name '*.go'
```

En Windows, si `winget` está disponible y no existe un `apt` real, también puede
resolverse una secuencia como:

```sh
apt update && apt install jq
```

sin delegar el parseo de `&&` a `cmd.exe` o PowerShell.

## Adaptador ejecutable

Se agrega `cmd/portablesh` para integración directa como shell de ejecución:

- `portablesh -c COMMAND [NAME [ARG...]]`;
- `portablesh FILE [ARG...]`;
- lectura de script desde stdin;
- `-n`/`--check` para validar sin ejecutar;
- `--version` y `--help`.

El adaptador usa el mismo `Runner`, ambiente y stdio del proceso. No detecta Git
ni cambia a Git Bash. Sigue siendo no interactivo: no se agregan prompt, history,
job control ni administración de terminal.

## Versión

La ampliación cambia el conjunto de comandos reservados antes del lookup
externo y agrega un ejecutable público, por lo que se publica como `v0.3.0`.
Los handlers mantienen prioridad para reducir impacto de embedding.

## Validación requerida

Además de la validación histórica:

- pruebas de coreutils con `ExternalDisabled` y `PATH` vacío;
- pruebas de `&&` y `;` con operaciones reales de filesystem;
- prueba de prioridad de handler frente a una utilidad portable homónima;
- pruebas de `RootDir` sobre operaciones de creación;
- pruebas de protección de `cp`/`mv` sobre el mismo path;
- pruebas unitarias de traducciones Windows/macOS;
- prueba de reautorización de comandos traducidos por `Policy`;
- build del adaptador `cmd/portablesh` y compilación cruzada de la suite.
