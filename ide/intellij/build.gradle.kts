plugins {
    id("java")
    id("org.jetbrains.kotlin.jvm") version "2.1.0"
    id("org.jetbrains.intellij") version "1.17.4"
}

val ideaVersion: String by project
val ideaType: String by project
val antlr4Version: String by project
val antlr4AdaptorVersion: String by project
val pluginVersion: String by project

group = "org.gala"
version = System.getenv("GALA_VERSION")?.removePrefix("v") ?: pluginVersion

repositories {
    mavenCentral()
}

dependencies {
    implementation("org.antlr:antlr4-intellij-adaptor:$antlr4AdaptorVersion")
    implementation("org.antlr:antlr4-runtime:$antlr4Version")

    testImplementation("junit:junit:4.13.2")
}

// Configure Gradle IntelliJ Plugin
intellij {
    version.set(ideaVersion)
    type.set(ideaType)
    plugins.set(listOf("org.jetbrains.plugins.go"))
}

// Include Bazel-generated ANTLR Java sources
sourceSets {
    main {
        java {
            srcDir("src/main/gen")
        }
    }
    // Exclude test sources when test framework is not available
    test {
        kotlin {
            setSrcDirs(emptyList<String>())
        }
    }
}

tasks {
    withType<JavaCompile> {
        sourceCompatibility = "17"
        targetCompatibility = "17"
    }
    withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile> {
        kotlinOptions.jvmTarget = "17"
    }

    buildSearchableOptions {
        enabled = false
    }

    patchPluginXml {
        sinceBuild.set("251")
        untilBuild.set("265.*")
    }

    signPlugin {
        certificateChain.set(System.getenv("CERTIFICATE_CHAIN"))
        privateKey.set(System.getenv("PRIVATE_KEY"))
        password.set(System.getenv("PRIVATE_KEY_PASSWORD"))
    }

    publishPlugin {
        token.set(System.getenv("PUBLISH_TOKEN"))
    }
}
