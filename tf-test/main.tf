terraform {
  required_version = ">= 1.4" # terraform_data

  required_providers {
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

# Sensitive result; changing the length replaces it (-/+).
resource "random_password" "db" {
  length  = var.password_length
  special = true
}

# Changing the greeting updates this in place (~).
resource "terraform_data" "greeting" {
  input = var.greeting
}

# Changing the release replaces this, creating the new one first (+/-).
# Its input contains a sensitive value.
resource "terraform_data" "release" {
  triggers_replace = var.release

  input = {
    version  = var.release
    password = random_password.db.result
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Changing the greeting replaces the file (-/+).
resource "local_file" "hello" {
  filename = "${path.module}/out/hello.txt"
  content  = "${var.greeting}\n"
}

# Reads the file back; shown as read (<=) whenever the file changes.
data "local_file" "hello" {
  filename = local_file.hello.filename
}

# Changing pet_count adds (+) or destroys (-) pets; changing pet_prefix replaces them.
module "pets" {
  source = "./modules/pets"

  pet_count = var.pet_count
  prefix    = var.pet_prefix
}

output "pet_names" {
  value = module.pets.names
}

resource "random_string" "import" {
  length = 3
}

output "random_str_value" {
  value = random_string.import
}
