# TMS Project Context & Architectural Guardrails

## 1. Project Background
- **Type**: B2B Transportation Management System (TMS) platform prototype.
- **Scope**: Involves a complex net of upstream (clients, manufacturers, order sources) and downstream (carriers, truck fleets, drivers) partners.
- **Data Reality**: Databases and libraries are currently incomplete/lacking. Schemas must handle sparse data, null fields, and optional relations with high resilience.

## 2. The AI-First Architecture (DO NOT DISRUPT)
- The bottom layer of this system relies heavily on **LangGraph, Tool Registries, state machines (Postgres checkpointer), and DeepSeek AI Agents**.
- **Rule**: When adding features, refactoring, or editing backend/frontend code, **NEVER disrupt or break the underlying AI integration, tool schemas, or state machine flows**.
- Tools registered in python (e.g. `analytics.query_metric`) are mapped directly to AI analysis capabilities; changing parameters or REST end-points requires updating corresponding schema registries.

## 3. Engineering & Iteration Guardrails
- **Surgical Code Modifications**: Only touch files directly related to the task. Never refactor surrounding code unless explicitly instructed.
- **Resilient Schemas**: When modifying Django models, ensure fields support optional values (`blank=True, null=True`) because upstream/downstream payloads are often incomplete.
- **State Machine Integrity**: Always follow established transaction locking (e.g., row-level locking during order dispatching) and status flow events to ensure data bloodline stays correct.
- **Verification**: Always run migrations, verify health/readiness endpoints, and seed demo data before concluding work.
