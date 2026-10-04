terraform {
  required_providers {
    random = {
      source = "hashicorp/random"
    }
  }
}

variable "pet_count" {
  type = number
}

variable "prefix" {
  type = string
}

resource "random_pet" "this" {
  count = var.pet_count

  prefix = var.prefix
  length = 2
}

output "names" {
  value = random_pet.this[*].id
}
