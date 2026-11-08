# Fila de Atendimento

MVP de senhas para pequenos balcoes de atendimento, implementado em **Go 1.23**, apenas com a biblioteca padrao.

## Funcionalidades

- Emite senhas sequenciais por ordem de chegada.
- Chama a primeira senha em espera para um guiche informado.
- Nao permite que uma senha seja atendida em dois guiches, nem que o mesmo guiche abra dois atendimentos simultaneos.
- Finaliza o atendimento ou registra ausencia, com historico consultavel em memoria.
- API HTTP JSON, validacao de metodos e testes concorrentes.

## Executar

```bash
go run .
```

O servidor fica em `http://127.0.0.1:8080` por padrao. Configure `ADDR` para alterar a escuta, por exemplo `ADDR=127.0.0.1:9090 go run .`.

```bash
curl -X POST http://127.0.0.1:8080/tickets
curl -X POST http://127.0.0.1:8080/tickets
curl -X POST http://127.0.0.1:8080/call   -H 'Content-Type: application/json' -d '{"desk":"guiche-1"}'
curl -X POST http://127.0.0.1:8080/resolve   -H 'Content-Type: application/json' -d '{"id":1,"status":"completed"}'
curl http://127.0.0.1:8080/tickets
```

Em `/resolve`, status permitido: `completed` ou `absent`. Chamadas invalidas retornam 400, e conflitos de estado retornam 409.

## Validacao

```bash
go test -race ./...
go vet ./...
```

O workflow CI executa os dois comandos em PRs. A verificacao de concorrencia usa mutex no mesmo processo.

## Limites

- **Armazenamento somente em memoria**: reiniciar perde as senhas; persistencia SQLite e recuperacao apos falhas estao no backlog.
- **Sem login nem autenticacao**: API deve permanecer local, nunca publica na internet sem autorizacao e protecoes apropriadas.
- Nao ha garantia de exclusao mutua entre *multiplos processos*. Usar um processo unico nesta primeira versao.
- Ainda nao possui HTMX, frontend ou painel de atendimento.


## Operacoes avancadas

A API oferece cancelamento de senhas ainda nao chamadas, devolucao de uma senha chamada para a fila, transferencia atomica entre guiches, posicao na fila, indicadores e paginacao com limite de 100 itens.

```bash
# Consultar estatisticas e guiches ativos
curl http://127.0.0.1:8080/stats

# Filtrar senhas e limitar o volume de resposta
curl 'http://127.0.0.1:8080/tickets/search?status=waiting&offset=0&limit=20'

# Consultar posicao de uma senha ainda aguardando
curl 'http://127.0.0.1:8080/tickets/position?id=2'

# Cancelar uma senha em espera
curl -X POST http://127.0.0.1:8080/cancel \
  -H 'Content-Type: application/json' -d '{"id":2}'

# Transferir atendimento ativo de um guiche a outro, se livre
curl -X POST http://127.0.0.1:8080/transfer \
  -H 'Content-Type: application/json' \
  -d '{"id":1,"source":"guiche-1","destination":"guiche-2"}'

# Devolver senha chamada para a fila, liberando o guiche
curl -X POST http://127.0.0.1:8080/requeue \
  -H 'Content-Type: application/json' -d '{"id":1}'

# Exportar snapshot versionado (nao inclui dados pessoais)
curl http://127.0.0.1:8080/export > backup.json
```

O metodo Go `RestoreQueue([]byte)` restaura um snapshot validado (versao 1), preservando a sequencia de senhas e impedindo guiches ativos duplicados. A importacao nao e disponibilizada por HTTP: ela deve ser integrada apenas a um procedimento administrativo controlado. O snapshot **nao e persistido automaticamente**; a memoria do processo continua sendo a fonte de verdade.

### Invariantes e seguranca

- Um guiche nao pode atender duas senhas simultaneamente.
- Senhas concluidas, ausentes ou canceladas nao podem ser chamadas novamente.
- Transferencias, cancelamentos e devolucoes usam a mesma exclusao mutua da fila.
- Identificadores de senhas nunca sao reutilizados; a paginacao e limitada a 100 registros.
- Snapshot rejeita IDs fora de ordem, estados invalidos, guiches ativos duplicados, campos inesperados e arquivos acima de 5 MiB.
- Endpoints de escrita **nao possuem autenticacao**. Mantenha o servidor vinculado a `127.0.0.1` ou use uma camada de autenticacao antes de qualquer exposicao externa.
- Para multiplas instancias, persistencia transacional e coordenacao distribuida ainda sao necessarias.

Os testes cobrem transicoes de estado, concorrencia com detector de race, snapshots corrompidos, paginacao, HTTP e isolamento de guiches.


## Autenticacao opcional e exposicao de rede

Por padrao, a API continua sem autenticacao **somente em loopback**. Ao configurar `ADDR` com um endereco publico ou de rede, o processo exige `API_TOKEN` antes de iniciar:

```bash
API_TOKEN='troque-por-um-segredo-longo' ADDR=0.0.0.0:8080 go run .
curl -H 'Authorization: Bearer troque-por-um-segredo-longo' http://127.0.0.1:8080/stats
```

Com token configurado, todas as rotas exceto `GET /health` exigem o cabecalho `Authorization: Bearer <token>`. Credenciais incorretas recebem HTTP 401 e `WWW-Authenticate: Bearer`. O token nao aparece em logs nem respostas. A comparacao utiliza `crypto/subtle`.

**Aviso:** bearer token em HTTP sem TLS pode ser interceptado. Para exposicao fora do host, utilize HTTPS em um proxy reverso confiavel, controle de acesso e protecao do segredo. O token nao substitui TLS, limites de requisicoes ou auditoria de operacoes.


## Checkpoints locais (biblioteca Go)

A fila disponibiliza `q.SaveSnapshotFile("queue.json")` e `LoadSnapshotFile("queue.json")` para backup e recuperacao explicitos. O arquivo e criado com permissoes `0600`, escrito em arquivo temporario, sincronizado e substituido por renomeacao atomica. O carregamento limita a leitura a 5 MiB e valida os invariantes antes de devolver uma fila utilizavel.

**Importante:** checkpoints sao manuais e nao sao executados automaticamente apos cada requisicao. Falhas do processo entre checkpoints podem causar perda de alteracoes recentes. O formato nao oferece coordenacao entre multiplos processos nem historico transacional; para producao, integrar persistencia transacional e autenticacao robusta.
