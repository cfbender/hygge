# TODOs
- clean up legacy provider code
  - do we need anthropic/openai since fantasy handles them?
- make sure small_model is used for title generation
  - the main token cost counts seem correct in the openrouter logs, but I see two other requests, one for 285 tokens input with 11 token output, and another after the first user request that is 306 tok input with 8 token output
- analyze and remove any remaining dead code, especially any legacy pre-fantasy/catwalk code
  - don't worry about backwards compatible config
