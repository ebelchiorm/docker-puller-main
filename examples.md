# Logging Examples

Esta aplicação agora suporta diferentes níveis de logging para otimizar a saída e reduzir o ruído nos logs.

## Modo Padrão (Default)

```bash
./puller --interval 60
```

**Saída típica:**
```
[INFO] Starting puller service with interval: 60s
[INFO] Cleanup enabled: false
[INFO] Label filtering enabled: false
[INFO] Check completed: 0 containers updated, 3 skipped
[INFO] Check completed: 1 containers updated, 2 skipped
[UPDATE] Successfully updated /myapp
```

## Modo Silencioso (--quiet) - **Recomendado para Produção**

```bash
./puller --interval 60 --quiet
```

**Saída típica:**
```
[INFO] Starting puller service with interval: 60s
[UPDATE] Restarting container /myapp with new image
[UPDATE] Successfully updated /myapp
[ERROR] Error restarting /nginx: container not found
```

**Benefícios:**
- ✅ Reduz drasticamente o volume de logs
- ✅ Mostra apenas o que realmente importa
- ✅ Ideal para monitoramento em produção
- ✅ Facilita identificação de problemas

## Modo Verboso (--verbose) - Para Debug

```bash
./puller --interval 60 --verbose
```

**Saída típica:**
```
[INFO] Starting puller service with interval: 60s
[INFO] Verbose logging enabled
[VERBOSE] Found 5 total containers, 2 eligible for updates
[VERBOSE] Skipping /redis (redis:latest): not from our registry
[VERBOSE] Checking container /myapp (registry.com/myapp:latest) with platform linux/amd64
[VERBOSE] New image downloaded for /myapp
[UPDATE] Restarting container /myapp with new image
[UPDATE] Successfully updated /myapp
[VERBOSE] Notification sent successfully
[VERBOSE] Check completed: no updates needed for 1 containers
```

## Comparação de Volume de Logs

### Cenário: 10 containers, 1 atualização por hora

| Modo | Logs por hora | Redução |
|------|---------------|---------|
| **Antigo** | ~200 linhas | - |
| **Padrão** | ~50 linhas | 75% |
| **Quiet** | ~5 linhas | **97.5%** |
| **Verbose** | ~300 linhas | Debug |

## Uso Recomendado

### Produção
```yaml
command: --interval 30 --cleanup --label-enable --quiet
```

### Desenvolvimento/Debug
```yaml
command: --interval 60 --verbose
```

### Monitoramento
```yaml
command: --interval 30 --cleanup --label-enable
# Modo padrão - equilibra informação com clareza
``` 