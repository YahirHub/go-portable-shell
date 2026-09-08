# 004. Literales UTF-8 v0.3.1

## Objetivo

Corregir la corrupción de texto Unicode al interpretar palabras sin comillas y literales con comillas dobles.

## Problema

El lexer indexa el source por bytes, lo cual es válido para conservar exactamente el script. Sin embargo, algunas rutas convertían cada byte con `string(byte)`. En Go esa conversión interpreta el byte como un punto de código Unicode y vuelve a codificarlo como UTF-8.

Por ejemplo, `ó` está formada por los bytes `C3 B3`. Convertir esos bytes por separado producía los caracteres `Ã³`. El mismo defecto afectaba comandos como:

```sh
printf "%s\n" "áéíóúñ ¿Qué pasó?"
git commit -m "Corregir integración"
```

Las comillas simples no mostraban el defecto porque esa ruta ya copiaba los bytes originales con `strings.Builder.WriteByte`.

## Solución

Se agrega una ruta única `addLiteralByte` que convierte el byte a una cadena de un byte mediante `string([]byte{value})` antes de concatenarlo. De esta manera las secuencias UTF-8 originales permanecen intactas hasta formar el string completo.

La corrección cubre:

- texto literal sin comillas;
- bytes escapados fuera de comillas;
- texto dentro de comillas dobles;
- bytes escapados dentro de comillas dobles.

No cambia el parser, las expansiones, el lookup de comandos ni el modelo de seguridad.

## Compatibilidad

La versión del contrato pasa de `0.3.0` a `0.3.1`. No hay cambios incompatibles de API pública.

## Pruebas

Se añade una regresión que ejecuta en una misma línea:

- `café` sin comillas;
- `vinculación áéíóúñ ¿Qué pasó?` con comillas dobles;
- `México` con comillas simples.

La salida debe conservar exactamente los bytes UTF-8 originales.

## Archivos importantes

- `lexer.go`
- `runner_test.go`
- `version.go`
- `README.md`

## Pendientes

Publicar/taggear `v0.3.1` cuando corresponda. Hasta entonces los consumidores que necesiten el fix de inmediato deben fijar una copia/reemplazo local validado.
