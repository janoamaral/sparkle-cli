# GitHub Actions - Build and Release

Se han configurado dos workflows de GitHub Actions para automatizar la compilación y creación de releases:

## Opción 1: `build-release.yml` - Basado en Commit Message (Recomendado para tu caso)

**Disparo:** Cuando se hace push a la rama `main`

**Detección:** Busca un patrón `v1.0.1` en el mensaje del commit

**Uso:**
```bash
git commit -m "Merge pull request... v1.0.2"
git push origin main
```

El workflow:
1. ✅ Extrae la versión del mensaje del commit
2. ✅ Genera builds para Linux, macOS (Intel y ARM) y Windows
3. ✅ Crea automáticamente un release con esa versión
4. ✅ Genera automáticamente release notes comparando commits
5. ✅ Carga todos los artefactos (binarios) al release

**Formato esperado del commit:**
- ✅ `v1.0.1` (simple)
- ✅ `Release v1.0.1`
- ✅ `Merge branch... v1.0.1`

---

## Opción 2: `release-by-tag.yml` - Basado en Git Tags

**Disparo:** Cuando se crea un tag con formato `v1.0.1`

**Uso:**
```bash
git tag v1.0.2
git push origin v1.0.2
```

O desde GitHub:
1. Ve a Releases
2. Click en "Draft a new release"
3. Ingresa el tag (v1.0.2)
4. GitHub Actions automáticamente creará el release

**Ventaja:** Más controlado y es el estándar en la industria

---

## Requisitos Previos

✅ Los workflows ya están configurados  
✅ Usan el go.mod existente (Go 1.26.2)  
✅ Se compila `./cmd/sparkle-cli/main.go`  

## Variables y Configuración

Si necesitas cambiar algo:
- **Go version:** Cambiar `1.26.2` en el workflow
- **Binarios de salida:** Están en formato `sparkle-cli-{OS}-{ARCH}`
- **Plataformas:** Linux amd64, macOS Intel (amd64), macOS ARM (arm64), Windows amd64

## Release Notes Automáticas

GitHub genera automáticamente las release notes comparando:
- Commits desde el release anterior
- PRs mergeadas
- Cambios en dependencias

## Artefactos Generados

Cada release tendrá:
- `sparkle-cli-linux-amd64` - Linux 64-bit
- `sparkle-cli-darwin-amd64` - macOS Intel
- `sparkle-cli-darwin-arm64` - macOS Apple Silicon
- `sparkle-cli-windows-amd64.exe` - Windows 64-bit

---

## Recomendación

Usa la **Opción 1** (`build-release.yml`) si prefieres que el versionado sea parte del mensaje de commit.

Usa la **Opción 2** (`release-by-tag.yml`) si prefieres el flujo estándar de Git tags (recomendado a largo plazo).

Puedes tener ambos activos simultáneamente sin conflicto.
