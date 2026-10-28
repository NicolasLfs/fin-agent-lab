class AgentError(Exception):
    """Base class for failures that have an explicit HTTP mapping."""


class ProviderRateLimitError(AgentError):
    pass


class ProviderUnavailableError(AgentError):
    pass


class UpstreamUnavailableError(AgentError):
    pass


class ServiceUnavailableError(AgentError):
    pass


class ToolValidationError(AgentError):
    pass


class ConfigurationError(AgentError):
    pass
