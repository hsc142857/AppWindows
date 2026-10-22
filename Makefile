.PHONY: list test build cli

# programs/ 아래 프로그램 이름 목록
PROGRAMS := $(notdir $(patsubst %/,%,$(wildcard programs/*/)))

list:
	@echo "관리 중인 프로그램:"
	@for p in $(PROGRAMS); do echo "  - $$p"; done

# 각 프로그램 Makefile에 해당 타깃이 있으면 순서대로 실행 (test는 공통 모듈 shared/도 포함)
# cli: Mac에서 바로 실행할 CLI를 각 프로그램 dist/에 만든다
test build cli:
	@if [ "$@" = test ]; then echo "==> shared"; (cd shared && go test ./...) || exit 1; fi
	@for p in $(PROGRAMS); do \
		if [ -f "programs/$$p/Makefile" ] && grep -q '^$@:' "programs/$$p/Makefile"; then \
			echo "==> $$p"; \
			$(MAKE) -C "programs/$$p" $@ || exit 1; \
		fi; \
	done
