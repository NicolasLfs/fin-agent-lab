variable "localstack_endpoint" {
  description = "The only permitted AWS-compatible endpoint for this local configuration."
  type        = string
  default     = "http://localhost:4566"

  validation {
    condition     = var.localstack_endpoint == "http://localhost:4566"
    error_message = "Terraform in this directory must target the local LocalStack endpoint http://localhost:4566."
  }
}

variable "queue_name_suffix" {
  description = "Optional suffix used to isolate ephemeral Terraform test queues."
  type        = string
  default     = ""

  validation {
    condition     = can(regex("^[A-Za-z0-9_-]{0,20}$", var.queue_name_suffix))
    error_message = "The queue name suffix may contain only letters, digits, underscores, and hyphens."
  }
}
