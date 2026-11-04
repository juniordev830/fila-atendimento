# Fila de Atendimento

> Status: concepção. Este repositório contém apenas o escopo inicial; ainda não há aplicação executável ou funcionalidades implementadas.

## Ideia

Controlar senhas e chamadas de pequenos balcões de atendimento.

## Primeira versão planejada

- [ ] Emitir senha sequencial.
- [ ] Chamar a próxima senha.
- [ ] Finalizar ou registrar ausência.

## Modelo inicial

Senha, fila e chamada.

## Regras e limites

Uma senha não pode ser chamada em dois guichês simultaneamente; primeira versão usa fila única por ordem de chegada.

Escopo mantido pequeno: sem pagamentos, integrações externas, infraestrutura de produção ou uso de dados pessoais reais nesta etapa.

## Tecnologia proposta

Go + HTMX + SQLite.

A escolha é uma proposta para a futura implementação; nenhuma dependência foi instalada e a tecnologia ainda não foi implementada.

## Critério de conclusão do primeiro incremento

Duas chamadas consecutivas devem selecionar senhas distintas na ordem correta.

## Próximos passos

1. Revisar este escopo antes de começar a implementação.
2. Preparar o ambiente mínimo da tecnologia escolhida.
3. Implementar um fluxo completo e validar o critério acima.
4. Documentar a execução e acrescentar exemplos demonstrativos.

## Possíveis evoluções

Depois de validar o fluxo principal, avaliar persistência, acessibilidade e uma interface mais completa conforme a plataforma. Evoluções não fazem parte da entrega atual.

## Como executar

Ainda não há comandos de execução. Eles serão adicionados quando existir uma primeira versão funcional.
