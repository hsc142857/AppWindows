.PHONY: list test build

# programs/ 아래 프로그램 이름 목록
PROGRAMS := $(notdir $(patsubst %/,%,$(wildcard programs/*/)))

list:
	@echo "관리 중인 프로그램:"
	@for p in $(PROGRAMS); do echo "  - $$p"; done

# 각 프로그램 Makefile에 해당 타깃이 있으면 순서대로 실행
test build:
	@for p in $(PROGRAMS); do \
		if [ -f "programs/$$p/Makefile" ] && grep -q '^$@:' "programs/$$p/Makefile"; then \
			echo "==> $$p"; \
			$(MAKE) -C "programs/$$p" $@ || exit 1; \
		fi; \
	done
