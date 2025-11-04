.PHONY := clear dev prod

COMPOSE := docker compose
COMPOSE_DEV := $(COMPOSE) -f compose.dev.yml

clear:
	$(COMPOSE_DEV) down --volumes --remove-orphans || true
	$(COMPOSE) down --volumes --remove-orphans || true
	rm -rf tmp

dev:
	$(COMPOSE_DEV) up --build

prod:
	$(COMPOSE) up --build
