# Roteiro de validação — Fila de Atendimento

O serviço Go mantém senhas e guichês em memória e expõe API HTTP local. Os módulos incluem `main.go`, `batch.go`, `reports.go`, `security.go`, `storage.go` e `queue_extensions.go`, com testes respectivos.

## Verificar

```bash
go test -race ./...
go run .
```

Em outra janela, consultar `http://127.0.0.1:8080` pelos exemplos documentados no README.

## Casos a proteger

- Senhas sequenciais e guichê já ocupado.
- Concorrência ao chamar a mesma senha, sem atendimento duplicado.
- Finalização vs ausência, histórico e emissão em lote.
- Falha de persistência, restauração parcial e relatório de fila vazia.
- Cabeçalhos de segurança e limites de requisição no endpoint local.

## Evolução

Manter regras em domínio testável; antes de uso em atendimento real, configurar autenticação por perfil, durabilidade transacional e regras de proteção de dados. Não depender de uma única instância em memória para estado crítico.
